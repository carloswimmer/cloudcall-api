package contact_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloudcall/internal/contact"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

func openDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	sqlDB, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	return sqlDB, ctx
}

func serve(t *testing.T, sqlDB *sql.DB, orgID uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	contact.NewHandler(&contact.Store{DB: sqlDB}, orgID).Register(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	return srv
}

// newSeededServer serves the seeded org. Read-only tests only.
func newSeededServer(t *testing.T) *httptest.Server {
	t.Helper()
	sqlDB, _ := openDB(t)
	return serve(t, sqlDB, db.OrganizationID)
}

// newIsolatedServer creates a throwaway organization with n contacts named
// "Person 01".."Person nn" so pagination tests never depend on shared rows.
func newIsolatedServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	sqlDB, ctx := openDB(t)
	orgID := uuid.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Contact Test Org', '#1F4E79', 'Europe/Berlin')`,
		orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM contacts WHERE organization_id = $1`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	})
	for i := 1; i <= n; i++ {
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT INTO contacts (id, organization_id, name, phone, email, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, NULL, now(), now())`,
			uuid.New(), orgID, fmt.Sprintf("Person %02d", i), fmt.Sprintf("+4930000%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return serve(t, sqlDB, orgID)
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, raw
}

type listBody struct {
	Items []struct {
		ID    string  `json:"id"`
		Name  string  `json:"name"`
		Phone string  `json:"phone"`
		Email *string `json:"email"`
	} `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

func decodeList(t *testing.T, raw []byte) listBody {
	t.Helper()
	var b listBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return b
}

func TestListContactsReturnsSeedContact(t *testing.T) {
	srv := newSeededServer(t)

	res, raw := get(t, srv.URL+"/api/v1/contacts")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	b := decodeList(t, raw)
	if b.Page != 1 || b.PageSize != 20 {
		t.Fatalf("page %d pageSize %d", b.Page, b.PageSize)
	}
	found := false
	for _, c := range b.Items {
		if c.ID == db.Contact1ID.String() {
			found = true
			if c.Name != "Ada Lovelace" || c.Phone != "+442071838750" || c.Email == nil || *c.Email != "ada@example.com" {
				t.Fatalf("seed contact %+v", c)
			}
		}
	}
	if !found || b.Total < 1 {
		t.Fatalf("seed contact missing: %s", raw)
	}
	if strings.Contains(string(raw), "organizationId") {
		t.Fatalf("organizationId leaked: %s", raw)
	}
}

func TestListContactsSearch(t *testing.T) {
	srv := newSeededServer(t)

	res, raw := get(t, srv.URL+"/api/v1/contacts?q=Ada")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	b := decodeList(t, raw)
	if b.Total < 1 || len(b.Items) < 1 || b.Items[0].ID != db.Contact1ID.String() {
		t.Fatalf("q=Ada: %s", raw)
	}

	// Phone search, case-insensitive name search.
	for _, q := range []string{"2071838", "lovelace"} {
		_, raw = get(t, srv.URL+"/api/v1/contacts?q="+q)
		if b := decodeList(t, raw); b.Total < 1 {
			t.Fatalf("q=%s: %s", q, raw)
		}
	}

	res, raw = get(t, srv.URL+"/api/v1/contacts?q=zzz")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	b = decodeList(t, raw)
	if b.Total != 0 || b.Items == nil || len(b.Items) != 0 {
		t.Fatalf("q=zzz must give empty items and total 0: %s", raw)
	}
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("items must serialize as []: %s", raw)
	}
}

func TestListContactsValidation(t *testing.T) {
	srv := newSeededServer(t)

	for _, q := range []string{
		"pageSize=0", "pageSize=101", "pageSize=abc", "page=0", "page=-1", "page=x",
		"sort=phone", "sort=-name", "sort=name,phone",
	} {
		res, raw := get(t, srv.URL+"/api/v1/contacts?"+q)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d: %s", q, res.StatusCode, raw)
		}
		var e struct {
			Code      string `json:"code"`
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(raw, &e); err != nil || e.Code != "validation_error" || e.RequestID == "" {
			t.Fatalf("%s: error %+v (%v): %s", q, e, err, raw)
		}
	}

	for _, q := range []string{"sort=name", "sort=", "pageSize=100", "pageSize=1"} {
		res, raw := get(t, srv.URL+"/api/v1/contacts?"+q)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %s", q, res.StatusCode, raw)
		}
	}
}

func TestListContactsPagination(t *testing.T) {
	srv := newIsolatedServer(t, 25)

	// Default pageSize is 20.
	_, raw := get(t, srv.URL+"/api/v1/contacts")
	b := decodeList(t, raw)
	if b.Total != 25 || len(b.Items) != 20 || b.Page != 1 || b.PageSize != 20 {
		t.Fatalf("default page: total=%d items=%d page=%d size=%d", b.Total, len(b.Items), b.Page, b.PageSize)
	}
	if b.Items[0].Name != "Person 01" || b.Items[19].Name != "Person 20" {
		t.Fatalf("order: %s ... %s", b.Items[0].Name, b.Items[19].Name)
	}

	_, raw = get(t, srv.URL+"/api/v1/contacts?page=2")
	b = decodeList(t, raw)
	if len(b.Items) != 5 || b.Items[0].Name != "Person 21" || b.Page != 2 || b.Total != 25 {
		t.Fatalf("page 2: %s", raw)
	}

	_, raw = get(t, srv.URL+"/api/v1/contacts?page=3&pageSize=10")
	b = decodeList(t, raw)
	if len(b.Items) != 5 || b.Items[0].Name != "Person 21" || b.PageSize != 10 {
		t.Fatalf("page 3 size 10: %s", raw)
	}

	// Page beyond the end: empty items, real total.
	_, raw = get(t, srv.URL+"/api/v1/contacts?page=9")
	b = decodeList(t, raw)
	if len(b.Items) != 0 || b.Total != 25 || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("page 9: %s", raw)
	}
}

func TestListContactsHugePageDoesNot500(t *testing.T) {
	srv := newSeededServer(t)
	hugePage := strconv.Itoa(math.MaxInt/contact.MaxPageSize + 2)
	res, raw := get(t, srv.URL+"/api/v1/contacts?page="+hugePage+"&pageSize=100")
	if res.StatusCode == http.StatusInternalServerError {
		t.Fatalf("huge page must not 500: %s", raw)
	}
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
}

func TestListContactsScopedToOrganization(t *testing.T) {
	srv := newIsolatedServer(t, 2)

	_, raw := get(t, srv.URL+"/api/v1/contacts?q=Ada")
	if b := decodeList(t, raw); b.Total != 0 {
		t.Fatalf("other org's contacts leaked: %s", raw)
	}
}

// send issues a request with an optional JSON body.
func send(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
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
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, raw
}

type contactBody struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Phone     string  `json:"phone"`
	Email     *string `json:"email"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type errorBody struct {
	Code        string            `json:"code"`
	FieldErrors map[string]string `json:"fieldErrors"`
	RequestID   string            `json:"requestId"`
}

func decodeContact(t *testing.T, raw []byte) contactBody {
	t.Helper()
	var c contactBody
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return c
}

func decodeError(t *testing.T, raw []byte) errorBody {
	t.Helper()
	var e errorBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return e
}

func createContact(t *testing.T, base, body string) contactBody {
	t.Helper()
	res, raw := send(t, http.MethodPost, base+"/api/v1/contacts", body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d: %s", res.StatusCode, raw)
	}
	return decodeContact(t, raw)
}

func TestCreateContact(t *testing.T) {
	srv := newIsolatedServer(t, 0)

	res, raw := send(t, http.MethodPost, srv.URL+"/api/v1/contacts",
		`{"name":"  Grace Hopper ","phone":"+14155550101","email":"grace@example.com"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	c := decodeContact(t, raw)
	if _, err := uuid.Parse(c.ID); err != nil || c.Name != "Grace Hopper" || c.Phone != "+14155550101" ||
		c.Email == nil || *c.Email != "grace@example.com" || c.CreatedAt == "" || c.UpdatedAt == "" {
		t.Fatalf("created %+v", c)
	}
	if strings.Contains(string(raw), "organizationId") {
		t.Fatalf("organizationId leaked: %s", raw)
	}

	// Email is optional: omitted and null both store no email.
	for i, body := range []string{
		`{"name":"No Email","phone":"+14155550102"}`,
		`{"name":"Null Email","phone":"+14155550103","email":null}`,
	} {
		c := createContact(t, srv.URL, body)
		if c.Email != nil {
			t.Fatalf("case %d: email %v", i, *c.Email)
		}
	}

	_, raw = get(t, srv.URL+"/api/v1/contacts")
	if b := decodeList(t, raw); b.Total != 3 {
		t.Fatalf("list after create: %s", raw)
	}
}

func TestCreateContactValidation(t *testing.T) {
	srv := newIsolatedServer(t, 0)

	cases := []struct {
		name, body string
		fields     []string
	}{
		{"bad phone", `{"name":"A","phone":"12345"}`, []string{"phone"}},
		{"missing phone", `{"name":"A"}`, []string{"phone"}},
		{"blank name", `{"name":"  ","phone":"+14155550101"}`, []string{"name"}},
		{"bad email", `{"name":"A","phone":"+14155550101","email":"nope"}`, []string{"email"}},
		{"all bad", `{"name":"","phone":"x","email":"y"}`, []string{"name", "phone", "email"}},
	}
	for _, tc := range cases {
		res, raw := send(t, http.MethodPost, srv.URL+"/api/v1/contacts", tc.body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d: %s", tc.name, res.StatusCode, raw)
		}
		e := decodeError(t, raw)
		if e.Code != "validation_error" || e.RequestID == "" {
			t.Fatalf("%s: %+v", tc.name, e)
		}
		for _, f := range tc.fields {
			if e.FieldErrors[f] == "" {
				t.Fatalf("%s: missing fieldErrors.%s: %s", tc.name, f, raw)
			}
		}
		if len(e.FieldErrors) != len(tc.fields) {
			t.Fatalf("%s: unexpected fieldErrors: %s", tc.name, raw)
		}
	}

	for _, body := range []string{`not json`, ``, `{"name":1,"phone":"+14155550101"}`} {
		res, raw := send(t, http.MethodPost, srv.URL+"/api/v1/contacts", body)
		if res.StatusCode != http.StatusBadRequest || decodeError(t, raw).Code != "validation_error" {
			t.Fatalf("body %q: status %d: %s", body, res.StatusCode, raw)
		}
	}
}

func TestCreateContactDuplicatePhone(t *testing.T) {
	srv := newIsolatedServer(t, 0)
	createContact(t, srv.URL, `{"name":"First","phone":"+14155550101"}`)

	res, raw := send(t, http.MethodPost, srv.URL+"/api/v1/contacts", `{"name":"Second","phone":"+14155550101"}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if e := decodeError(t, raw); e.Code != "conflict" || e.RequestID == "" {
		t.Fatalf("error %+v", e)
	}
}

func TestPatchContactNameOnlyKeepsPhone(t *testing.T) {
	srv := newIsolatedServer(t, 0)
	c := createContact(t, srv.URL, `{"name":"Old Name","phone":"+14155550101","email":"old@example.com"}`)

	res, raw := send(t, http.MethodPatch, srv.URL+"/api/v1/contacts/"+c.ID, `{"name":"New Name"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	u := decodeContact(t, raw)
	if u.ID != c.ID || u.Name != "New Name" || u.Phone != "+14155550101" ||
		u.Email == nil || *u.Email != "old@example.com" || u.CreatedAt != c.CreatedAt {
		t.Fatalf("patched %+v (was %+v)", u, c)
	}
	if u.UpdatedAt == c.UpdatedAt {
		t.Fatalf("updatedAt not changed: %s", raw)
	}
}

func TestPatchContactEmail(t *testing.T) {
	srv := newIsolatedServer(t, 0)
	c := createContact(t, srv.URL, `{"name":"Mail","phone":"+14155550101","email":"a@example.com"}`)
	url := srv.URL + "/api/v1/contacts/" + c.ID

	res, raw := send(t, http.MethodPatch, url, `{"email":"b@example.com"}`)
	if u := decodeContact(t, raw); res.StatusCode != http.StatusOK || u.Email == nil || *u.Email != "b@example.com" {
		t.Fatalf("set email: %d %s", res.StatusCode, raw)
	}

	// JSON null clears the email.
	res, raw = send(t, http.MethodPatch, url, `{"email":null}`)
	if u := decodeContact(t, raw); res.StatusCode != http.StatusOK || u.Email != nil || u.Name != "Mail" {
		t.Fatalf("clear email: %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"email":null`) {
		t.Fatalf("email must serialize as null: %s", raw)
	}

	// Invalid values are rejected and nothing changes.
	res, raw = send(t, http.MethodPatch, url, `{"name":"","phone":"bad","email":"bad"}`)
	e := decodeError(t, raw)
	if res.StatusCode != http.StatusBadRequest || e.Code != "validation_error" ||
		e.FieldErrors["name"] == "" || e.FieldErrors["phone"] == "" || e.FieldErrors["email"] == "" {
		t.Fatalf("invalid patch: %d %s", res.StatusCode, raw)
	}
	// An explicit null name or phone is invalid too (they are required columns).
	for _, body := range []string{`{"name":null}`, `{"phone":null}`} {
		res, raw = send(t, http.MethodPatch, url, body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status %d: %s", body, res.StatusCode, raw)
		}
	}
}

func TestPatchContactDuplicatePhone(t *testing.T) {
	srv := newIsolatedServer(t, 0)
	createContact(t, srv.URL, `{"name":"One","phone":"+14155550101"}`)
	two := createContact(t, srv.URL, `{"name":"Two","phone":"+14155550102"}`)

	res, raw := send(t, http.MethodPatch, srv.URL+"/api/v1/contacts/"+two.ID, `{"phone":"+14155550101"}`)
	if res.StatusCode != http.StatusConflict || decodeError(t, raw).Code != "conflict" {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
}

func TestPatchAndDeleteUnknownContact(t *testing.T) {
	srv := newIsolatedServer(t, 0)

	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		res, raw := send(t, http.MethodPatch, srv.URL+"/api/v1/contacts/"+id, `{"name":"X"}`)
		if res.StatusCode != http.StatusNotFound || decodeError(t, raw).Code != "not_found" {
			t.Fatalf("patch %s: status %d: %s", id, res.StatusCode, raw)
		}
		res, raw = send(t, http.MethodDelete, srv.URL+"/api/v1/contacts/"+id, "")
		if res.StatusCode != http.StatusNotFound || decodeError(t, raw).Code != "not_found" {
			t.Fatalf("delete %s: status %d: %s", id, res.StatusCode, raw)
		}
	}
}

func TestDeleteContactThenListExcludesIt(t *testing.T) {
	srv := newIsolatedServer(t, 0)
	c := createContact(t, srv.URL, `{"name":"Gone","phone":"+14155550101"}`)

	res, raw := send(t, http.MethodDelete, srv.URL+"/api/v1/contacts/"+c.ID, "")
	if res.StatusCode != http.StatusNoContent || len(raw) != 0 {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	_, raw = get(t, srv.URL+"/api/v1/contacts")
	if b := decodeList(t, raw); b.Total != 0 || len(b.Items) != 0 {
		t.Fatalf("deleted contact still listed: %s", raw)
	}

	// Second delete is 404; the phone can be reused.
	res, _ = send(t, http.MethodDelete, srv.URL+"/api/v1/contacts/"+c.ID, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status %d", res.StatusCode)
	}
	createContact(t, srv.URL, `{"name":"Again","phone":"+14155550101"}`)
}

func TestWritesAreScopedToOrganization(t *testing.T) {
	srv := newIsolatedServer(t, 0)

	// Ada belongs to the seeded org, not this server's organization.
	id := db.Contact1ID.String()
	res, _ := send(t, http.MethodPatch, srv.URL+"/api/v1/contacts/"+id, `{"name":"Hacked"}`)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("patch status %d", res.StatusCode)
	}
	res, _ = send(t, http.MethodDelete, srv.URL+"/api/v1/contacts/"+id, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete status %d", res.StatusCode)
	}
	_, raw := get(t, newSeededServer(t).URL+"/api/v1/contacts?q=Ada")
	if b := decodeList(t, raw); b.Total < 1 || b.Items[0].Name != "Ada Lovelace" {
		t.Fatalf("seed contact changed: %s", raw)
	}
}
