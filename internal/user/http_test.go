package user_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

// newServer mounts the handlers for the seeded demo user. Use it for read-only
// tests only: the demo row is shared with other packages' tests.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	sqlDB, _ := openDB(t)
	return serve(t, sqlDB, db.OrganizationID, db.DemoUserID)
}

// newIsolatedServer creates a throwaway organization and user so mutating tests
// never touch the shared demo rows (packages run in parallel against one DB).
func newIsolatedServer(t *testing.T) (*httptest.Server, uuid.UUID) {
	t.Helper()
	sqlDB, ctx := openDB(t)
	orgID, userID := uuid.New(), uuid.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Test Org', '#1F4E79', 'Europe/Berlin')`,
		orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Test User', '900', 'available', 1)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	})
	return serve(t, sqlDB, orgID, userID), userID
}

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

func serve(t *testing.T, sqlDB *sql.DB, orgID, userID uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	user.NewHandler(&user.Store{DB: sqlDB}, orgID, userID).Register(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	return res, buf.Bytes()
}

type errBody struct {
	Code        string            `json:"code"`
	FieldErrors map[string]string `json:"fieldErrors"`
	RequestID   string            `json:"requestId"`
}

func TestGetMe(t *testing.T) {
	srv := newServer(t)

	res, raw := do(t, http.MethodGet, srv.URL+"/api/v1/me", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var body struct {
		User struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Extension string `json:"extension"`
			Presence  string `json:"presence"`
			Version   int    `json:"version"`
		} `json:"user"`
		Organization struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			BrandColor string `json:"brandColor"`
			Timezone   string `json:"timezone"`
		} `json:"organization"`
		DemoMode bool `json:"demoMode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if !body.DemoMode {
		t.Fatal("demoMode must be true")
	}
	if body.User.ID != db.DemoUserID.String() || body.User.Name != "Alex Rivera" ||
		body.User.Extension != "100" || body.User.Presence != "available" || body.User.Version != 1 {
		t.Fatalf("user %+v", body.User)
	}
	if body.Organization.ID != db.OrganizationID.String() || body.Organization.Name != "Northwind" ||
		body.Organization.BrandColor != "#1F4E79" || body.Organization.Timezone != "Europe/Berlin" {
		t.Fatalf("organization %+v", body.Organization)
	}
	if strings.Contains(string(raw), "organizationId") || strings.Contains(string(raw), "presenceBeforeBusy") {
		t.Fatalf("internal fields leaked: %s", raw)
	}
}

func TestListUsers(t *testing.T) {
	srv := newServer(t)

	res, raw := do(t, http.MethodGet, srv.URL+"/api/v1/users", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var body struct {
		Users []struct {
			ID        string `json:"id"`
			Extension string `json:"extension"`
		} `json:"users"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Users) != 3 {
		t.Fatalf("want 3 users, got %d: %s", len(body.Users), raw)
	}
	exts := map[string]string{}
	for _, u := range body.Users {
		exts[u.Extension] = u.ID
	}
	if exts["100"] != db.DemoUserID.String() || exts["101"] != db.Colleague1ID.String() ||
		exts["102"] != db.Colleague2ID.String() {
		t.Fatalf("users %v", exts)
	}
}

func TestPatchPresence(t *testing.T) {
	srv, _ := newIsolatedServer(t)
	url := srv.URL + "/api/v1/me/presence"

	res, raw := do(t, http.MethodPatch, url, `{"presence":"offline","expectedVersion":1}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var u struct {
		Presence string `json:"presence"`
		Version  int    `json:"version"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatal(err)
	}
	if u.Presence != "offline" || u.Version != 2 {
		t.Fatalf("user %+v", u)
	}

	res, raw = do(t, http.MethodPatch, url, `{"presence":"offline","expectedVersion":1}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var e errBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != "version_mismatch" || e.RequestID == "" {
		t.Fatalf("error %+v", e)
	}
}

func TestPatchPresenceValidation(t *testing.T) {
	srv, _ := newIsolatedServer(t)
	url := srv.URL + "/api/v1/me/presence"

	res, raw := do(t, http.MethodPatch, url, `{"presence":"dancing","expectedVersion":1}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var e errBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != "validation_error" || e.FieldErrors["presence"] == "" {
		t.Fatalf("error %+v", e)
	}

	res, raw = do(t, http.MethodPatch, url, `{"presence":"busy","expectedVersion":0}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.FieldErrors["expectedVersion"] == "" {
		t.Fatalf("error %+v (%v)", e, err)
	}

	res, raw = do(t, http.MethodPatch, url, `{not json`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Code != "validation_error" {
		t.Fatalf("error %+v (%v)", e, err)
	}

	// Validation failures must not change state.
	res, raw = do(t, http.MethodGet, srv.URL+"/api/v1/me", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"version":1`) {
		t.Fatalf("state changed: %s", raw)
	}
}

func TestPatchPresenceIgnoresClientIdentity(t *testing.T) {
	srv, userID := newIsolatedServer(t)
	body := `{"presence":"busy","expectedVersion":1,"userId":"` + db.Colleague1ID.String() +
		`","organizationId":"00000000-0000-0000-0000-000000000000"}`
	res, raw := do(t, http.MethodPatch, srv.URL+"/api/v1/me/presence", body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), userID.String()) {
		t.Fatalf("expected configured user updated: %s", raw)
	}
	if strings.Contains(string(raw), db.Colleague1ID.String()) {
		t.Fatalf("client-supplied userId must be ignored: %s", raw)
	}
}
