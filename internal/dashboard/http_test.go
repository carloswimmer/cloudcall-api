package dashboard_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
	"time"

	"cloudcall/internal/dashboard"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// env owns a throwaway organization and owner so counts never depend on rows
// created by tests in other packages that share the seeded demo organization.
type env struct {
	db    *sql.DB
	ctx   context.Context
	org   uuid.UUID
	owner uuid.UUID
	srv   *httptest.Server
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

func serve(t *testing.T, sqlDB *sql.DB, org, owner uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	dashboard.NewHandler(sqlDB, org, owner).Register(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	return srv
}

func newEnv(t *testing.T, tz string) *env {
	t.Helper()
	sqlDB, ctx := openDB(t)
	e := &env{db: sqlDB, ctx: ctx, org: uuid.New(), owner: uuid.New()}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Dashboard Test Org', '#1F4E79', $2)`,
		e.org, tz); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, e.org)
	})
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Dash Owner', '900', 'available', 1)`, e.owner, e.org); err != nil {
		t.Fatal(err)
	}
	e.srv = serve(t, sqlDB, e.org, e.owner)
	return e
}

// insertCall inserts a call whose created_at is the SQL expression createdAt
// (for example "now()" or "now() - interval '1 day'"). Non-terminal calls are
// owned by owner; terminal calls are owned by it too, which the partial unique
// index allows because it only covers non-terminal statuses.
func (e *env) insertCall(t *testing.T, owner uuid.UUID, direction, status, createdAt string) {
	t.Helper()
	if _, err := e.db.ExecContext(e.ctx, `
		INSERT INTO calls (id, organization_id, owner_user_id, peer_name_snapshot, peer_phone_snapshot,
		                   direction, status, version, created_at)
		VALUES ($1, $2, $3, 'Peer', '+4930000000', $4, $5, 1, `+createdAt+`)`,
		uuid.New(), e.org, owner, direction, status); err != nil {
		t.Fatal(err)
	}
}

type body struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
	Total    int    `json:"total"`
	Inbound  int    `json:"inbound"`
	Outbound int    `json:"outbound"`
	Missed   int    `json:"missed"`
	Active   int    `json:"active"`
}

func get(t *testing.T, srv *httptest.Server) (int, body, map[string]any) {
	t.Helper()
	res, err := http.Get(srv.URL + "/api/v1/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	var out body
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out, raw
}

func TestDashboardCountsTodayInUTC(t *testing.T) {
	e := newEnv(t, "UTC")
	// Today: one inbound missed, one outbound ended.
	e.insertCall(t, e.owner, "inbound", "missed", "now()")
	e.insertCall(t, e.owner, "outbound", "ended", "now()")
	// Yesterday: must not count toward today's totals.
	e.insertCall(t, e.owner, "outbound", "ended", "now() - interval '1 day'")
	// One ringing call today (non-terminal) counts as active and as today's inbound.
	e.insertCall(t, e.owner, "inbound", "ringing", "now()")

	status, got, _ := get(t, e.srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	want := time.Now().UTC().Format("2006-01-02")
	if got.Date != want {
		t.Errorf("date = %q, want %q", got.Date, want)
	}
	if got.Timezone != "UTC" {
		t.Errorf("timezone = %q", got.Timezone)
	}
	if got.Total != 3 || got.Inbound != 2 || got.Outbound != 1 || got.Missed != 1 || got.Active != 1 {
		t.Errorf("counts = %+v, want total=3 inbound=2 outbound=1 missed=1 active=1", got)
	}
}

func TestDashboardEmptyOrganizationIsZeroed(t *testing.T) {
	e := newEnv(t, "UTC")
	status, got, raw := get(t, e.srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got.Total != 0 || got.Inbound != 0 || got.Outbound != 0 || got.Missed != 0 || got.Active != 0 {
		t.Errorf("counts = %+v, want all zero", got)
	}
	for _, k := range []string{"date", "timezone", "total", "inbound", "outbound", "missed", "active"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q in %v", k, raw)
		}
	}
}

// Active ignores the day boundary: a call that started yesterday and is still
// ringing/active counts as active but not toward today's totals.
func TestDashboardActiveIsNotLimitedToToday(t *testing.T) {
	e := newEnv(t, "UTC")
	e.insertCall(t, e.owner, "outbound", "active", "now() - interval '2 days'")
	_, got, _ := get(t, e.srv)
	if got.Active != 1 || got.Total != 0 {
		t.Errorf("counts = %+v, want active=1 total=0", got)
	}
}

// Another user's live call is not "my" active call, and another organization's
// calls are never counted.
func TestDashboardActiveIsOwnerScopedAndOrgScoped(t *testing.T) {
	e := newEnv(t, "UTC")
	otherUser := uuid.New()
	if _, err := e.db.ExecContext(e.ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Colleague', '901', 'available', 1)`, otherUser, e.org); err != nil {
		t.Fatal(err)
	}
	e.insertCall(t, otherUser, "inbound", "ringing", "now()")

	other := newEnv(t, "UTC")
	other.insertCall(t, other.owner, "outbound", "dialing", "now()")

	_, got, _ := get(t, e.srv)
	if got.Active != 0 {
		t.Errorf("active = %d, want 0 (colleague call and foreign org must not count)", got.Active)
	}
	if got.Total != 1 {
		t.Errorf("total = %d, want 1 (organization-wide, own org only)", got.Total)
	}
}

// The day boundary follows the organization's timezone, not UTC. Midnight of the
// current local day belongs to today; one second earlier belongs to yesterday.
func TestDashboardDayBoundaryFollowsOrganizationTimezone(t *testing.T) {
	for _, tz := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago", "Europe/Berlin"} {
		t.Run(tz, func(t *testing.T) {
			e := newEnv(t, tz)
			localMidnight := `((now() AT TIME ZONE '` + tz + `')::date::timestamp AT TIME ZONE '` + tz + `')`
			e.insertCall(t, e.owner, "outbound", "ended", localMidnight)
			e.insertCall(t, e.owner, "inbound", "missed", localMidnight+` - interval '1 second'`)

			_, got, _ := get(t, e.srv)
			if got.Timezone != tz {
				t.Errorf("timezone = %q", got.Timezone)
			}
			if got.Total != 1 || got.Outbound != 1 || got.Inbound != 0 || got.Missed != 0 {
				t.Errorf("counts = %+v, want only the call at local midnight", got)
			}
			var wantDate string
			if err := e.db.QueryRowContext(e.ctx,
				`SELECT to_char((now() AT TIME ZONE $1)::date, 'YYYY-MM-DD')`, tz).Scan(&wantDate); err != nil {
				t.Fatal(err)
			}
			if got.Date != wantDate {
				t.Errorf("date = %q, want %q", got.Date, wantDate)
			}
		})
	}
}

// Seeded organization smoke test: timezone comes from the seed and the route
// answers with the contract shape. Counts are not asserted because other
// packages' tests also write calls for the demo user.
func TestDashboardSeededOrganization(t *testing.T) {
	sqlDB, _ := openDB(t)
	srv := serve(t, sqlDB, db.OrganizationID, db.DemoUserID)
	status, got, _ := get(t, srv)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got.Timezone != "Europe/Berlin" {
		t.Errorf("timezone = %q, want Europe/Berlin", got.Timezone)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(got.Date) {
		t.Errorf("date = %q, want YYYY-MM-DD", got.Date)
	}
	if got.Total < got.Inbound || got.Total < got.Outbound || got.Total < got.Missed {
		t.Errorf("inconsistent counts: %+v", got)
	}
}

func TestDashboardUnknownOrganizationIs500NotLeak(t *testing.T) {
	sqlDB, _ := openDB(t)
	srv := serve(t, sqlDB, uuid.New(), uuid.New())
	res, err := http.Get(srv.URL + "/api/v1/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	var eb httpx.ErrorBody
	if err := json.NewDecoder(res.Body).Decode(&eb); err != nil {
		t.Fatal(err)
	}
	if eb.Code != "internal_error" || eb.RequestID == "" {
		t.Errorf("error body = %+v", eb)
	}
}
