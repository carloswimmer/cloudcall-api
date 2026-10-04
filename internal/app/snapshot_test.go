package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/event"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

// snapEnv is an isolated organization wired like App.New: shared hub, loader and
// stores. The demo rows are never touched (other packages assert on them).
type snapEnv struct {
	ctx    context.Context
	hub    *event.Hub
	calls  *call.Store
	users  *user.Store
	org    uuid.UUID
	owner  uuid.UUID
	clock  time.Time
	loader snapshotLoader
}

func newSnapEnv(t *testing.T) *snapEnv {
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

	e := &snapEnv{
		ctx:   ctx,
		org:   uuid.New(),
		owner: uuid.New(),
		clock: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	}
	seedIsolated(t, ctx, sqlDB, e)

	e.users = &user.Store{DB: sqlDB}
	e.calls = &call.Store{DB: sqlDB, Users: e.users, Now: func() time.Time { return e.clock }}
	e.loader = snapshotLoader{calls: e.calls, users: e.users, orgID: e.org, ownerID: e.owner, now: func() time.Time { return e.clock }}
	e.hub = event.NewHub(e.loader, time.Hour, func() time.Time { return e.clock })
	e.calls.Publisher, e.users.Publisher = e.hub, e.hub
	t.Cleanup(e.hub.Close)
	return e
}

func seedIsolated(t *testing.T, ctx context.Context, sqlDB *sql.DB, e *snapEnv) {
	t.Helper()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Snapshot Org', '#1F4E79', 'Europe/Berlin')`,
		e.org); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Snap Owner', '910', 'available', 1), ($3, $2, 'Snap Colleague', '911', 'offline', 1)`,
		e.owner, e.org, uuid.New()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM call_transitions WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, e.org)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, e.org)
	})
}

func recvEvent(t *testing.T, out <-chan event.Envelope) event.Envelope {
	t.Helper()
	select {
	case e, ok := <-out:
		if !ok {
			t.Fatal("stream closed")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return event.Envelope{}
}

type snapBody struct {
	Calls      []struct{ ID, Status string }
	Users      []struct{ Name string }
	Tombstones []event.Tombstone
}

func decode(t *testing.T, e event.Envelope) snapBody {
	t.Helper()
	if e.Type != event.TypeSnapshot {
		t.Fatalf("type = %q, want snapshot", e.Type)
	}
	var b snapBody
	if err := json.Unmarshal(e.Payload, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoaderReturnsActiveCallsUsersAndRecentTombstones(t *testing.T) {
	e := newSnapEnv(t)

	// One call ended 5 minutes ago, one ended an hour ago, one is live.
	old, err := e.calls.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Old", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.calls.Apply(e.ctx, e.org, old.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(55 * time.Minute)
	recent, err := e.calls.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Recent", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.calls.Apply(e.ctx, e.org, recent.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(5 * time.Minute)
	live, err := e.calls.Create(e.ctx, e.org, e.owner, call.DirectionInbound, nil, "Live", "+12025550100")
	if err != nil {
		t.Fatal(err)
	}

	snap, err := e.loader.LoadSnapshot(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Calls) != 1 {
		t.Fatalf("calls = %+v", snap.Calls)
	}
	raw, _ := json.Marshal(snap.Calls[0])
	var c struct {
		ID     uuid.UUID `json:"id"`
		Status string    `json:"status"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.ID != live.ID || c.Status != "ringing" {
		t.Fatalf("call = %s (%v)", raw, err)
	}
	if len(snap.Users) != 2 {
		t.Fatalf("users = %+v", snap.Users)
	}
	if len(snap.Tombstones) != 1 || snap.Tombstones[0].ID != recent.ID ||
		snap.Tombstones[0].Version != 2 || snap.Tombstones[0].Status != "ended" {
		t.Fatalf("tombstones = %+v, want only the call that ended within 15 minutes", snap.Tombstones)
	}
}

func TestSubscribeAfterCommitsSeesSnapshotThenLiveCallAndPresence(t *testing.T) {
	e := newSnapEnv(t)
	_, out, cancel := e.hub.Subscribe(e.ctx)
	defer cancel()

	snap := decode(t, recvEvent(t, out))
	if len(snap.Calls) != 0 || len(snap.Users) != 2 || len(snap.Tombstones) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}

	c, err := e.calls.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Ada", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]event.Envelope{}
	for i := 0; i < 2; i++ {
		ev := recvEvent(t, out)
		got[ev.Type] = ev
	}
	if ev := got[event.TypeCallUpdated]; ev.EntityID == nil || *ev.EntityID != c.ID.String() {
		t.Fatalf("call event = %+v", ev)
	}
	if ev := got[event.TypePresenceUpdated]; ev.EntityID == nil || *ev.EntityID != e.owner.String() {
		t.Fatalf("presence event = %+v", ev)
	}

	// Ending the call: the next client sees a tombstone and no active call.
	if _, err := e.calls.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	_, out2, cancel2 := e.hub.Subscribe(e.ctx)
	defer cancel2()
	snap2 := decode(t, recvEvent(t, out2))
	if len(snap2.Calls) != 0 || len(snap2.Tombstones) != 1 || snap2.Tombstones[0].ID != c.ID || snap2.Tombstones[0].Version != 2 {
		t.Fatalf("snapshot after end = %+v", snap2)
	}
}
