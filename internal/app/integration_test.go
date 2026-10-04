package app_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudcall/internal/app"
	"cloudcall/internal/call"
	"cloudcall/internal/contact"
	"cloudcall/internal/platform/config"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/platform/httpx"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

// TestShutdownEndsOpenEventStream proves the graceful path used by cmd/api: with
// an SSE client connected, http.Server.Shutdown must return before its deadline
// (the hub closes the stream) and Serve must return http.ErrServerClosed.
func TestShutdownEndsOpenEventStream(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	a, err := app.New(context.Background(), config.Config{DatabaseURL: url, DemoMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := a.NewServer("", "http://localhost:4200")
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	res, err := http.Get("http://" + ln.Addr().String() + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// Wait for the snapshot so the stream is really established.
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if sc.Text() == "" {
			break
		}
	}

	streamEnded := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, res.Body)
		streamEnded <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an open SSE stream: %v", err)
	}
	select {
	case <-streamEnded:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE response did not end")
	}
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// intEnv is an isolated organization with the real call handler (including demo
// routes). Concurrent tests never touch the shared demo rows that other packages
// assert on while go test ./... runs packages in parallel.
type intEnv struct {
	ctx   context.Context
	db    *sql.DB
	store *call.Store
	org   uuid.UUID
	owner uuid.UUID
	srv   *httptest.Server
}

func newIntEnv(t *testing.T) *intEnv {
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

	e := &intEnv{ctx: ctx, db: sqlDB, org: uuid.New(), owner: uuid.New()}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Integration Org', '#1F4E79', 'Europe/Berlin')`,
		e.org); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Integration Owner', '920', 'available', 1)`, e.owner, e.org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM call_notes WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM call_transitions WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, e.org)
	})

	users := &user.Store{DB: sqlDB}
	e.store = &call.Store{DB: sqlDB, Users: users}
	h := call.NewHandler(e.store, &contact.Store{DB: sqlDB}, e.org, e.owner)
	mux := http.NewServeMux()
	h.Register(mux)
	h.RegisterDemo(mux)
	e.srv = httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *intEnv) postJSON(t *testing.T, path, body string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(e.srv.URL+path, "application/json", strings.NewReader(body))
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

type racedPost struct {
	status int
	body   map[string]any
}

// racePosts fires n identical POSTs at the same instant.
func racePosts(t *testing.T, n int, url, body string) []racedPost {
	t.Helper()
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		got   = make([]racedPost, n)
		errs  = make([]error, n)
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := http.Post(url, "application/json", strings.NewReader(body))
			if err != nil {
				errs[i] = err
				return
			}
			defer res.Body.Close()
			var out map[string]any
			if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
				errs[i] = err
				return
			}
			got[i] = racedPost{status: res.StatusCode, body: out}
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func countStatus(results []racedPost, want int) int {
	n := 0
	for _, r := range results {
		if r.status == want {
			n++
		}
	}
	return n
}

func TestConcurrentAnswerOnSameInboundCallOnlyOneWins(t *testing.T) {
	e := newIntEnv(t)
	for i := 0; i < 10; i++ {
		status, body := e.postJSON(t, "/api/v1/demo/incoming-call", `{"phone":"+14155550101"}`)
		if status != http.StatusCreated {
			t.Fatalf("iteration %d: incoming status %d body %v", i, status, body)
		}
		id, err := uuid.Parse(body["id"].(string))
		if err != nil {
			t.Fatalf("iteration %d: id %v", i, body["id"])
		}

		results := racePosts(t, 2, e.srv.URL+"/api/v1/calls/"+id.String()+"/actions",
			`{"action":"answer","expectedVersion":1}`)
		if countStatus(results, http.StatusOK) != 1 || countStatus(results, http.StatusConflict) != 1 {
			t.Fatalf("iteration %d: %+v, want one 200 and one 409", i, results)
		}

		var toActive int
		if err := e.db.QueryRowContext(e.ctx,
			`SELECT count(*) FROM call_transitions WHERE call_id = $1 AND to_status = 'active'`, id).Scan(&toActive); err != nil {
			t.Fatal(err)
		}
		if toActive != 1 {
			t.Fatalf("iteration %d: %d transitions to active, want 1", i, toActive)
		}

		if _, err := e.store.Apply(e.ctx, e.org, id, call.Command{Action: call.ActionEnd, ExpectedVersion: 2}); err != nil {
			t.Fatalf("iteration %d: end: %v", i, err)
		}
	}
}

func TestConcurrentCreateCallOnlyOneWins(t *testing.T) {
	e := newIntEnv(t)
	for i := 0; i < 10; i++ {
		results := racePosts(t, 2, e.srv.URL+"/api/v1/calls", `{"phone":"+14155550100"}`)
		if countStatus(results, http.StatusCreated) != 1 || countStatus(results, http.StatusConflict) != 1 {
			t.Fatalf("iteration %d: %+v, want one 201 and one 409", i, results)
		}
		var conflictCode string
		for _, r := range results {
			if r.status == http.StatusConflict {
				conflictCode, _ = r.body["code"].(string)
			}
		}
		if conflictCode != "active_call_exists" {
			t.Fatalf("iteration %d: 409 code %q, want active_call_exists", i, conflictCode)
		}

		var n int
		if err := e.db.QueryRowContext(e.ctx,
			`SELECT count(*) FROM calls WHERE owner_user_id = $1 AND status IN ('dialing','ringing','active')`, e.owner).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("iteration %d: %d non-terminal calls, want 1", i, n)
		}
		var id uuid.UUID
		if err := e.db.QueryRowContext(e.ctx,
			`SELECT id FROM calls WHERE owner_user_id = $1 AND status = 'dialing'`, e.owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.Apply(e.ctx, e.org, id, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
			t.Fatalf("iteration %d: end: %v", i, err)
		}
	}
}

// A process that died mid-call leaves non-terminal rows behind. The sweep that
// runs at startup fails them and frees the owner; the app then reports ready.
func TestRestartSweepThenReady(t *testing.T) {
	e := newIntEnv(t)
	status, body := e.postJSON(t, "/api/v1/calls", `{"phone":"+14155550100"}`)
	if status != http.StatusCreated {
		t.Fatalf("create leftover: %d %v", status, body)
	}
	leftover, err := uuid.Parse(body["id"].(string))
	if err != nil {
		t.Fatalf("id %v", body["id"])
	}

	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}

	got, _, _, err := e.store.Get(e.ctx, e.org, leftover)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != call.StatusFailed || got.FailureReason == nil || *got.FailureReason != call.ReasonSimulationInterrupted {
		t.Fatalf("after sweep: status %q reason %v", got.Status, got.FailureReason)
	}
	if has, err := e.store.HasNonTerminal(e.ctx, e.org, e.owner); err != nil || has {
		t.Fatalf("non-terminal after sweep: has=%v err=%v", has, err)
	}

	// "Restart": a fresh App sweeps the demo organization and must come up ready.
	a, err := app.New(e.ctx, config.Config{DatabaseURL: os.Getenv("DATABASE_URL"), DemoMode: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ready status %d", res.StatusCode)
	}
}
