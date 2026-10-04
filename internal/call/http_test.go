package call_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
