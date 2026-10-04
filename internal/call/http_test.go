package call_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/contact"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// httpEnv serves the call routes for the isolated organization and owner of a storeEnv.
type httpEnv struct {
	*storeEnv
	srv     *httptest.Server
	contact uuid.UUID
}

func newHTTPEnv(t *testing.T) *httpEnv {
	t.Helper()
	e := newStoreEnv(t)
	contactID := uuid.New()
	if _, err := e.db.ExecContext(e.ctx,
		`INSERT INTO contacts (id, organization_id, name, phone, created_at, updated_at)
		 VALUES ($1, $2, 'Grace Hopper', '+442071838750', now(), now())`, contactID, e.org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.db.Exec(`DELETE FROM contacts WHERE organization_id = $1`, e.org) })

	h := call.NewHandler(e.store, &contact.Store{DB: e.db}, e.org, e.owner)
	mux := http.NewServeMux()
	h.Register(mux)
	h.RegisterDemo(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	return &httpEnv{storeEnv: e, srv: srv, contact: contactID}
}

// post sends a JSON body and decodes the JSON answer into a generic map.
func (e *httpEnv) post(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(e.srv.URL+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return res.StatusCode, out
}

func wantStatus(t *testing.T, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("status %d, want %d (body %v)", got, want, body)
	}
}

func wantCode(t *testing.T, body map[string]any, code string) {
	t.Helper()
	if body["code"] != code {
		t.Fatalf("code %v, want %s (body %v)", body["code"], code, body)
	}
}

// createOutbound starts an outbound call by phone and returns its ID.
func (e *httpEnv) createOutbound(t *testing.T) string {
	t.Helper()
	status, body := e.post(t, "/api/v1/calls", `{"phone":"+14155550100"}`)
	wantStatus(t, status, http.StatusCreated, body)
	return body["id"].(string)
}

func (e *httpEnv) createInbound(t *testing.T) string {
	t.Helper()
	status, body := e.post(t, "/api/v1/demo/incoming-call", `{"phone":"+14155550101"}`)
	wantStatus(t, status, http.StatusCreated, body)
	return body["id"].(string)
}

func TestCreateOutboundByPhone(t *testing.T) {
	e := newHTTPEnv(t)
	status, body := e.post(t, "/api/v1/calls", `{"phone":"+14155550100"}`)
	wantStatus(t, status, http.StatusCreated, body)
	if body["status"] != "dialing" || body["direction"] != "outbound" || body["version"] != float64(1) {
		t.Fatalf("unexpected call %v", body)
	}
	if body["peerName"] != "Unknown" || body["peerPhone"] != "+14155550100" || body["contactId"] != nil {
		t.Fatalf("unexpected peer %v", body)
	}
	if _, err := uuid.Parse(body["id"].(string)); err != nil {
		t.Fatalf("id %v", body["id"])
	}
}

func TestCreateOutboundByContact(t *testing.T) {
	e := newHTTPEnv(t)
	status, body := e.post(t, "/api/v1/calls", `{"contactId":"`+e.contact.String()+`"}`)
	wantStatus(t, status, http.StatusCreated, body)
	if body["peerName"] != "Grace Hopper" || body["peerPhone"] != "+442071838750" || body["contactId"] != e.contact.String() {
		t.Fatalf("unexpected peer %v", body)
	}
}

func TestSecondCreateConflicts(t *testing.T) {
	e := newHTTPEnv(t)
	e.createOutbound(t)
	status, body := e.post(t, "/api/v1/calls", `{"phone":"+14155550102"}`)
	wantStatus(t, status, http.StatusConflict, body)
	wantCode(t, body, "active_call_exists")

	// The demo incoming call is blocked the same way.
	status, body = e.post(t, "/api/v1/demo/incoming-call", `{"phone":"+14155550103"}`)
	wantStatus(t, status, http.StatusConflict, body)
	wantCode(t, body, "active_call_exists")
}

func TestCreateRejectsBadBodies(t *testing.T) {
	e := newHTTPEnv(t)
	cases := map[string]string{
		"both":           `{"contactId":"` + uuid.NewString() + `","phone":"+14155550100"}`,
		"neither":        `{}`,
		"bad phone":      `{"phone":"12345"}`,
		"bad contact id": `{"contactId":"nope"}`,
		"not json":       `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			status, out := e.post(t, "/api/v1/calls", body)
			wantStatus(t, status, http.StatusBadRequest, out)
			wantCode(t, out, "validation_error")
			if name == "both" || name == "neither" {
				fields, _ := out["fieldErrors"].(map[string]any)
				if fields["contactId"] == nil || fields["phone"] == nil {
					t.Fatalf("fieldErrors %v", out["fieldErrors"])
				}
			}
		})
	}
	// Same validation on the demo route.
	status, out := e.post(t, "/api/v1/demo/incoming-call", `{}`)
	wantStatus(t, status, http.StatusBadRequest, out)
	wantCode(t, out, "validation_error")

	// Nothing was created along the way.
	has, err := e.store.HasNonTerminal(e.ctx, e.org, e.owner)
	if err != nil || has {
		t.Fatalf("has=%v err=%v", has, err)
	}
}

func TestCreateUnknownContactIsNotFound(t *testing.T) {
	e := newHTTPEnv(t)
	status, body := e.post(t, "/api/v1/calls", `{"contactId":"`+uuid.NewString()+`"}`)
	wantStatus(t, status, http.StatusNotFound, body)
	wantCode(t, body, "not_found")

	// A contact of another organization is just as unknown.
	other := uuid.New()
	var otherOrg uuid.UUID
	if err := e.db.QueryRowContext(e.ctx, `SELECT id FROM organizations WHERE id <> $1 LIMIT 1`, e.org).Scan(&otherOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(e.ctx,
		`INSERT INTO contacts (id, organization_id, name, phone, created_at, updated_at)
		 VALUES ($1, $2, 'Elsewhere', '+4915112345678', now(), now())`, other, otherOrg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.db.Exec(`DELETE FROM contacts WHERE id = $1`, other) })
	status, body = e.post(t, "/api/v1/calls", `{"contactId":"`+other.String()+`"}`)
	wantStatus(t, status, http.StatusNotFound, body)
	wantCode(t, body, "not_found")

	// No call was left behind and the owner can still dial.
	e.createOutbound(t)
}

func TestIncomingCallIsRinging(t *testing.T) {
	e := newHTTPEnv(t)
	status, body := e.post(t, "/api/v1/demo/incoming-call", `{"contactId":"`+e.contact.String()+`"}`)
	wantStatus(t, status, http.StatusCreated, body)
	if body["status"] != "ringing" || body["direction"] != "inbound" || body["peerName"] != "Grace Hopper" {
		t.Fatalf("unexpected call %v", body)
	}
}

func TestDemoConnectOutbound(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createOutbound(t)

	status, body := e.post(t, "/api/v1/demo/calls/"+id+"/connect", `{"expectedVersion":1}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "ringing" || body["version"] != float64(2) {
		t.Fatalf("unexpected call %v", body)
	}

	status, body = e.post(t, "/api/v1/demo/calls/"+id+"/connect", `{"expectedVersion":2}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "active" || body["version"] != float64(3) || body["startedAt"] == nil {
		t.Fatalf("unexpected call %v", body)
	}

	status, body = e.post(t, "/api/v1/calls/"+id+"/actions", `{"action":"end","expectedVersion":3}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "ended" || body["endedAt"] == nil {
		t.Fatalf("unexpected call %v", body)
	}
}

func TestAnswerInbound(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createInbound(t)
	status, body := e.post(t, "/api/v1/calls/"+id+"/actions", `{"action":"answer","expectedVersion":1}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "active" || body["version"] != float64(2) {
		t.Fatalf("unexpected call %v", body)
	}
}

func TestRejectInbound(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createInbound(t)
	status, body := e.post(t, "/api/v1/calls/"+id+"/actions", `{"action":"reject","expectedVersion":1}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "rejected" {
		t.Fatalf("unexpected call %v", body)
	}
	// The owner is free again.
	e.createOutbound(t)
}

func TestFailInboundRingTimeoutIsMissed(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createInbound(t)
	status, body := e.post(t, "/api/v1/demo/calls/"+id+"/fail", `{"expectedVersion":1,"reason":"ring_timeout"}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["status"] != "missed" || body["failureReason"] != "ring_timeout" {
		t.Fatalf("unexpected call %v", body)
	}
}

func TestFailRejectsBadReasons(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createInbound(t)
	for name, reason := range map[string]string{
		"reserved":  `"simulation_interrupted"`,
		"wrong dir": `"no_answer"`,
		"unknown":   `"cosmic_rays"`,
		"empty":     `""`,
	} {
		t.Run(name, func(t *testing.T) {
			status, body := e.post(t, "/api/v1/demo/calls/"+id+"/fail", `{"expectedVersion":1,"reason":`+reason+`}`)
			wantStatus(t, status, http.StatusBadRequest, body)
			wantCode(t, body, "validation_error")
		})
	}
	status, body := e.post(t, "/api/v1/demo/calls/"+id+"/fail", `{"expectedVersion":1}`)
	wantStatus(t, status, http.StatusBadRequest, body)
	wantCode(t, body, "validation_error")
}

func TestActionErrors(t *testing.T) {
	e := newHTTPEnv(t)
	out := e.createOutbound(t)

	t.Run("stale version", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"end","expectedVersion":7}`)
		wantStatus(t, status, http.StatusConflict, body)
		wantCode(t, body, "version_mismatch")
	})
	t.Run("invalid transition", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"answer","expectedVersion":1}`)
		wantStatus(t, status, http.StatusConflict, body)
		wantCode(t, body, "invalid_transition")
	})
	t.Run("simulator action is not a user action", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"connect","expectedVersion":1}`)
		wantStatus(t, status, http.StatusBadRequest, body)
		wantCode(t, body, "validation_error")
	})
	t.Run("unknown action", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"explode","expectedVersion":1}`)
		wantStatus(t, status, http.StatusBadRequest, body)
		wantCode(t, body, "validation_error")
	})
	t.Run("bad expected version", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"end"}`)
		wantStatus(t, status, http.StatusBadRequest, body)
		wantCode(t, body, "validation_error")
	})
	t.Run("terminal call", func(t *testing.T) {
		status, body := e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"end","expectedVersion":1}`)
		wantStatus(t, status, http.StatusOK, body)
		status, body = e.post(t, "/api/v1/calls/"+out+"/actions", `{"action":"end","expectedVersion":2}`)
		wantStatus(t, status, http.StatusConflict, body)
		wantCode(t, body, "invalid_transition")
	})
}

func TestMissingCallIsNotFound(t *testing.T) {
	e := newHTTPEnv(t)
	for _, tc := range []struct{ path, body string }{
		{"/api/v1/calls/" + uuid.NewString() + "/actions", `{"action":"end","expectedVersion":1}`},
		{"/api/v1/demo/calls/" + uuid.NewString() + "/connect", `{"expectedVersion":1}`},
		{"/api/v1/demo/calls/" + uuid.NewString() + "/fail", `{"expectedVersion":1,"reason":"network_error"}`},
		{"/api/v1/calls/not-a-uuid/actions", `{"action":"end","expectedVersion":1}`},
	} {
		t.Run(strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			status, body := e.post(t, tc.path, tc.body)
			wantStatus(t, status, http.StatusNotFound, body)
			wantCode(t, body, "not_found")
		})
	}
}

func TestRegisterDoesNotMountDemoRoutes(t *testing.T) {
	e := newStoreEnv(t)
	h := call.NewHandler(e.store, &contact.Store{DB: e.db}, e.org, e.owner)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	res, err := http.Post(srv.URL+"/api/v1/demo/incoming-call", "application/json",
		strings.NewReader(`{"phone":"+14155550100"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
}

// do sends any request with an optional JSON body and decodes the JSON answer.
func (e *httpEnv) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return res.StatusCode, out
}

func (e *httpEnv) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	return e.do(t, http.MethodGet, path, "")
}

// act applies a user action at the given fake time.
func (e *httpEnv) act(t *testing.T, id, action string, version int) map[string]any {
	t.Helper()
	status, body := e.post(t, "/api/v1/calls/"+id+"/actions",
		`{"action":"`+action+`","expectedVersion":`+strconv.Itoa(version)+`}`)
	wantStatus(t, status, http.StatusOK, body)
	return body
}

// advance moves the store clock forward.
func (e *httpEnv) advance(d time.Duration) { e.clock = e.clock.Add(d) }

// ids returns the call IDs of a list answer in order.
func ids(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items is not an array: %v", body)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["id"].(string))
	}
	return out
}

func sameIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ids %v, want %v", got, want)
		}
	}
}

// seedHistory creates four calls, each an hour apart, oldest first:
// c1 outbound ended, c2 inbound rejected, c3 inbound missed, c4 outbound dialing.
func seedHistory(t *testing.T, e *httpEnv) (c1, c2, c3, c4 string) {
	t.Helper()
	c1 = e.createOutbound(t)
	e.act(t, c1, "end", 1)

	e.advance(time.Hour)
	c2 = e.createInbound(t)
	e.act(t, c2, "reject", 1)

	e.advance(time.Hour)
	c3 = e.createInbound(t)
	status, body := e.post(t, "/api/v1/demo/calls/"+c3+"/fail", `{"expectedVersion":1,"reason":"ring_timeout"}`)
	wantStatus(t, status, http.StatusOK, body)

	e.advance(time.Hour)
	c4 = e.createOutbound(t)
	return
}

func TestListCallsFilters(t *testing.T) {
	e := newHTTPEnv(t)
	c1, c2, c3, c4 := seedHistory(t, e)
	t0 := e.clock.Add(-3 * time.Hour) // created_at of c1

	cases := []struct {
		name  string
		query string
		want  []string
		total float64
	}{
		{"all, newest first", "", []string{c4, c3, c2, c1}, 4},
		{"inbound", "?direction=inbound", []string{c3, c2}, 2},
		{"outbound", "?direction=outbound", []string{c4, c1}, 2},
		{"status ended", "?status=ended", []string{c1}, 1},
		{"status dialing", "?status=dialing", []string{c4}, 1},
		{"terminal", "?terminal=true", []string{c3, c2, c1}, 3},
		{"not terminal", "?terminal=false", []string{c4}, 1},
		{"impossible combination is empty", "?status=ended&terminal=false", []string{}, 0},
		{"direction and status", "?direction=inbound&status=missed", []string{c3}, 1},
		{"from is inclusive", "?from=" + t0.Add(time.Hour).Format(time.RFC3339), []string{c4, c3, c2}, 3},
		{"to is inclusive", "?to=" + t0.Add(2*time.Hour).Format(time.RFC3339), []string{c3, c2, c1}, 3},
		{"from and to", "?from=" + t0.Add(time.Hour).Format(time.RFC3339) + "&to=" + t0.Add(2*time.Hour).Format(time.RFC3339), []string{c3, c2}, 2},
		{"empty window", "?from=" + t0.Add(10*time.Hour).Format(time.RFC3339), []string{}, 0},
		{"page 2", "?pageSize=2&page=2", []string{c2, c1}, 4},
		{"page beyond the end", "?pageSize=2&page=3", []string{}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := e.get(t, "/api/v1/calls"+tc.query)
			wantStatus(t, status, http.StatusOK, body)
			sameIDs(t, ids(t, body), tc.want...)
			if body["total"] != tc.total {
				t.Fatalf("total %v, want %v", body["total"], tc.total)
			}
		})
	}

	t.Run("envelope", func(t *testing.T) {
		_, body := e.get(t, "/api/v1/calls?pageSize=2&page=2")
		if body["page"] != float64(2) || body["pageSize"] != float64(2) {
			t.Fatalf("envelope %v", body)
		}
		first := body["items"].([]any)[0].(map[string]any)
		if first["peerName"] != "Unknown" || first["direction"] != "inbound" || first["status"] != "rejected" {
			t.Fatalf("item %v", first)
		}
	})
}

func TestListCallsTiesBreakByIDDescending(t *testing.T) {
	e := newHTTPEnv(t)
	// The clock never moves, so every created_at is equal.
	a := e.createOutbound(t)
	e.act(t, a, "end", 1)
	b := e.createOutbound(t)
	e.act(t, b, "end", 1)

	_, body := e.get(t, "/api/v1/calls")
	got := ids(t, body)
	want := []string{a, b}
	if b > a {
		want = []string{b, a}
	}
	sameIDs(t, got, want...)
}

func TestListCallsRejectsBadQuery(t *testing.T) {
	e := newHTTPEnv(t)
	cases := map[string]string{
		"direction": "direction=sideways",
		"status":    "status=nope",
		"terminal":  "terminal=maybe",
		"from":      "from=yesterday",
		"to":        "to=2026-10-04",
		"page":      "page=0",
		"pageSize":  "pageSize=101",
	}
	for field, q := range cases {
		t.Run(field, func(t *testing.T) {
			status, body := e.get(t, "/api/v1/calls?"+q)
			wantStatus(t, status, http.StatusBadRequest, body)
			wantCode(t, body, "validation_error")
			fields, _ := body["fieldErrors"].(map[string]any)
			if fields[field] == nil {
				t.Fatalf("fieldErrors %v, want %s", body["fieldErrors"], field)
			}
		})
	}

	// Huge page values are a validation error, never a 500.
	status, body := e.get(t, "/api/v1/calls?page=9223372036854775807&pageSize=100")
	wantStatus(t, status, http.StatusBadRequest, body)
}

func TestListCallsIsScopedToOrganization(t *testing.T) {
	e := newHTTPEnv(t)
	e.createOutbound(t)

	other := call.NewHandler(e.store, &contact.Store{DB: e.db}, uuid.New(), uuid.New())
	mux := http.NewServeMux()
	other.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/v1/calls")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || body["total"] != float64(0) {
		t.Fatalf("status %d body %v", res.StatusCode, body)
	}
	status, _ := e.get(t, "/api/v1/calls/"+uuid.NewString())
	if status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
}

func TestCallDetailHasTransitionsAndNote(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createOutbound(t)

	e.advance(time.Second)
	status, body := e.post(t, "/api/v1/demo/calls/"+id+"/connect", `{"expectedVersion":1}`)
	wantStatus(t, status, http.StatusOK, body)
	e.advance(time.Second)
	status, body = e.post(t, "/api/v1/demo/calls/"+id+"/connect", `{"expectedVersion":2}`)
	wantStatus(t, status, http.StatusOK, body)
	e.advance(90 * time.Second)
	e.act(t, id, "end", 3)

	status, body = e.get(t, "/api/v1/calls/"+id)
	wantStatus(t, status, http.StatusOK, body)
	if body["id"] != id || body["status"] != "ended" || body["version"] != float64(4) {
		t.Fatalf("call %v", body)
	}
	if body["note"] != nil {
		t.Fatalf("note %v, want null", body["note"])
	}
	trs, ok := body["transitions"].([]any)
	if !ok || len(trs) != 4 {
		t.Fatalf("transitions %v", body["transitions"])
	}
	want := []struct{ from, to any }{{nil, "dialing"}, {"dialing", "ringing"}, {"ringing", "active"}, {"active", "ended"}}
	for i, w := range want {
		tr := trs[i].(map[string]any)
		if tr["from"] != w.from || tr["to"] != w.to || tr["occurredAt"] == nil {
			t.Fatalf("transition %d: %v, want %v", i, tr, w)
		}
	}

	status, put := e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"Asked for a quote"}`)
	wantStatus(t, status, http.StatusOK, put)
	status, body = e.get(t, "/api/v1/calls/"+id)
	wantStatus(t, status, http.StatusOK, body)
	note, _ := body["note"].(map[string]any)
	if note["text"] != "Asked for a quote" || note["updatedAt"] == nil {
		t.Fatalf("note %v", body["note"])
	}
}

func TestCallDetailMissing(t *testing.T) {
	e := newHTTPEnv(t)
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		status, body := e.get(t, "/api/v1/calls/"+id)
		wantStatus(t, status, http.StatusNotFound, body)
		wantCode(t, body, "not_found")
	}
}

func TestDurationSeconds(t *testing.T) {
	e := newHTTPEnv(t)

	// Never answered: no startedAt, so no duration even though it ended.
	rejected := e.createInbound(t)
	e.advance(5 * time.Second)
	rej := e.act(t, rejected, "reject", 1)
	if _, has := rej["durationSeconds"]; has || rej["endedAt"] == nil {
		t.Fatalf("rejected call %v", rej)
	}

	// Active: startedAt without endedAt.
	e.advance(time.Second)
	id := e.createInbound(t)
	e.advance(time.Second)
	active := e.act(t, id, "answer", 1)
	if _, has := active["durationSeconds"]; has || active["startedAt"] == nil {
		t.Fatalf("active call %v", active)
	}

	// Ended after 90.9 seconds: whole seconds, truncated.
	e.advance(90*time.Second + 900*time.Millisecond)
	ended := e.act(t, id, "end", 2)
	if ended["durationSeconds"] != float64(90) {
		t.Fatalf("ended call %v", ended)
	}

	// The same value shows up in list and detail.
	_, list := e.get(t, "/api/v1/calls?status=ended")
	if got := list["items"].([]any)[0].(map[string]any)["durationSeconds"]; got != float64(90) {
		t.Fatalf("list duration %v", got)
	}
	_, detail := e.get(t, "/api/v1/calls/"+id)
	if detail["durationSeconds"] != float64(90) {
		t.Fatalf("detail duration %v", detail["durationSeconds"])
	}
	_, detail = e.get(t, "/api/v1/calls/"+rejected)
	if _, has := detail["durationSeconds"]; has {
		t.Fatalf("rejected detail %v", detail)
	}
}

func TestNoteRejectedWhileNotTerminal(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createInbound(t) // ringing
	status, body := e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"too early"}`)
	wantStatus(t, status, http.StatusConflict, body)
	wantCode(t, body, "invalid_transition")

	e.act(t, id, "answer", 1) // active
	status, body = e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"still talking"}`)
	wantStatus(t, status, http.StatusConflict, body)
	wantCode(t, body, "invalid_transition")

	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT count(*) FROM call_notes WHERE call_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("notes %d err %v", n, err)
	}
}

func TestNoteUpsertOnTerminalCall(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createOutbound(t)
	e.act(t, id, "end", 1)

	status, body := e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"first"}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["text"] != "first" || body["updatedAt"] == nil {
		t.Fatalf("note %v", body)
	}

	e.advance(time.Minute)
	status, body = e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"second"}`)
	wantStatus(t, status, http.StatusOK, body)
	if body["text"] != "second" {
		t.Fatalf("note %v", body)
	}

	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT count(*) FROM call_notes WHERE call_id = $1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notes %d err %v", n, err)
	}
	_, detail := e.get(t, "/api/v1/calls/"+id)
	if detail["note"].(map[string]any)["text"] != "second" {
		t.Fatalf("detail %v", detail)
	}

	// An empty text clears the note's content but keeps the row.
	status, body = e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":""}`)
	wantStatus(t, status, http.StatusOK, body)
}

func TestNoteOnEveryTerminalStatus(t *testing.T) {
	e := newHTTPEnv(t)
	c1, c2, c3, _ := seedHistory(t, e)
	for _, id := range []string{c1, c2, c3} { // ended, rejected, missed
		status, body := e.do(t, http.MethodPut, "/api/v1/calls/"+id+"/note", `{"text":"ok"}`)
		wantStatus(t, status, http.StatusOK, body)
	}
}

func TestNoteValidation(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createOutbound(t)
	e.act(t, id, "end", 1)
	path := "/api/v1/calls/" + id + "/note"

	t.Run("2000 characters fit", func(t *testing.T) {
		status, body := e.do(t, http.MethodPut, path, `{"text":"`+strings.Repeat("é", 2000)+`"}`)
		wantStatus(t, status, http.StatusOK, body)
	})
	for name, body := range map[string]string{
		"2001 characters": `{"text":"` + strings.Repeat("a", 2001) + `"}`,
		"missing text":    `{}`,
		"null text":       `{"text":null}`,
		"number text":     `{"text":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			status, out := e.do(t, http.MethodPut, path, body)
			wantStatus(t, status, http.StatusBadRequest, out)
			wantCode(t, out, "validation_error")
			fields, _ := out["fieldErrors"].(map[string]any)
			if name != "number text" && fields["text"] == nil {
				t.Fatalf("fieldErrors %v", out["fieldErrors"])
			}
		})
	}
	t.Run("not json", func(t *testing.T) {
		status, out := e.do(t, http.MethodPut, path, `{`)
		wantStatus(t, status, http.StatusBadRequest, out)
		wantCode(t, out, "validation_error")
	})
	t.Run("unknown call", func(t *testing.T) {
		status, out := e.do(t, http.MethodPut, "/api/v1/calls/"+uuid.NewString()+"/note", `{"text":"x"}`)
		wantStatus(t, status, http.StatusNotFound, out)
		wantCode(t, out, "not_found")
	})
	t.Run("malformed id", func(t *testing.T) {
		status, out := e.do(t, http.MethodPut, "/api/v1/calls/nope/note", `{"text":"x"}`)
		wantStatus(t, status, http.StatusNotFound, out)
	})
}

func TestCallOfAnotherOrganizationIsNotFound(t *testing.T) {
	e := newHTTPEnv(t)
	id := e.createOutbound(t)

	// A second handler bound to another organization must not see the call.
	other := call.NewHandler(e.store, &contact.Store{DB: e.db}, uuid.New(), uuid.New())
	mux := http.NewServeMux()
	other.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	res, err := http.Post(srv.URL+"/api/v1/calls/"+id+"/actions", "application/json",
		strings.NewReader(`{"action":"end","expectedVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", res.StatusCode)
	}
}
