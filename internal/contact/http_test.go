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
