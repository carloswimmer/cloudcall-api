package call_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/platform/db"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

// storeEnv is an isolated organization with one user so tests never touch the
// shared demo rows (packages run in parallel against one database).
type storeEnv struct {
	ctx   context.Context
	db    *sql.DB
	store *call.Store
	users *user.Store
	org   uuid.UUID
	owner uuid.UUID
	clock time.Time
}

func newStoreEnv(t *testing.T) *storeEnv {
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

	env := &storeEnv{
		ctx:   ctx,
		db:    sqlDB,
		users: &user.Store{DB: sqlDB},
		org:   uuid.New(),
		owner: uuid.New(),
		clock: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	}
	env.store = &call.Store{DB: sqlDB, Users: env.users, Now: func() time.Time { return env.clock }}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Call Test Org', '#1F4E79', 'Europe/Berlin')`,
		env.org); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Call Tester', '901', 'available', 1)`, env.owner, env.org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM call_notes WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, env.org)
		_, _ = sqlDB.Exec(`DELETE FROM call_transitions WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, env.org)
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, env.org)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, env.org)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, env.org)
	})
	return env
}

func (e *storeEnv) advance(d time.Duration) { e.clock = e.clock.Add(d) }

func (e *storeEnv) ownerUser(t *testing.T) user.User {
	t.Helper()
	u, err := e.users.Get(e.ctx, e.org, e.owner)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *storeEnv) createOutbound(t *testing.T) call.Call {
	t.Helper()
	c, err := e.store.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Ada Lovelace", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *storeEnv) createInbound(t *testing.T) call.Call {
	t.Helper()
	c, err := e.store.Create(e.ctx, e.org, e.owner, call.DirectionInbound, nil, "Grace Hopper", "+12025550100")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *storeEnv) countCalls(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT count(*) FROM calls WHERE organization_id = $1`, e.org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateOutboundStartsDialingAndMarksOwnerBusy(t *testing.T) {
	e := newStoreEnv(t)
	contact := db.Contact1ID

	c, err := e.store.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, &contact, "Ada Lovelace", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != call.StatusDialing || c.Version != 1 || c.Direction != call.DirectionOutbound {
		t.Fatalf("call %+v", c)
	}
	if c.OwnerUserID != e.owner || c.OrganizationID != e.org {
		t.Fatalf("ownership %+v", c)
	}
	if c.ContactID == nil || *c.ContactID != contact {
		t.Fatalf("contact %v", c.ContactID)
	}
	if !c.CreatedAt.Equal(e.clock) || c.StartedAt != nil || c.EndedAt != nil || c.FailureReason != nil {
		t.Fatalf("timestamps %+v", c)
	}

	got, ts, note, err := e.store.Get(e.ctx, e.org, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if note != nil {
		t.Fatalf("note %+v", note)
	}
	if got.PeerNameSnapshot != "Ada Lovelace" || got.PeerPhoneSnapshot != "+442071838750" || got.Status != call.StatusDialing {
		t.Fatalf("stored call %+v", got)
	}
	if len(ts) != 1 || ts[0].From != nil || ts[0].To != call.StatusDialing || !ts[0].OccurredAt.Equal(e.clock) {
		t.Fatalf("transitions %+v", ts)
	}

	u := e.ownerUser(t)
	if u.Presence != user.PresenceBusy || u.Version != 2 {
		t.Fatalf("owner %+v", u)
	}
	if u.PresenceBeforeBusy == nil || *u.PresenceBeforeBusy != user.PresenceAvailable {
		t.Fatalf("presence_before_busy %v", u.PresenceBeforeBusy)
	}
}

func TestCreateInboundStartsRinging(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createInbound(t)
	if c.Status != call.StatusRinging || c.Version != 1 || c.Direction != call.DirectionInbound || c.ContactID != nil {
		t.Fatalf("call %+v", c)
	}
	if u := e.ownerUser(t); u.Presence != user.PresenceBusy {
		t.Fatalf("owner %+v", u)
	}
}

func TestCreateKeepsOriginalPresenceBeforeBusy(t *testing.T) {
	e := newStoreEnv(t)
	if _, err := e.users.SetPresence(e.ctx, e.org, e.owner, user.PresenceOffline, 1); err != nil {
		t.Fatal(err)
	}
	c := e.createOutbound(t)
	u := e.ownerUser(t)
	if u.PresenceBeforeBusy == nil || *u.PresenceBeforeBusy != user.PresenceOffline {
		t.Fatalf("presence_before_busy %v", u.PresenceBeforeBusy)
	}

	if _, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	u = e.ownerUser(t)
	if u.Presence != user.PresenceOffline || u.PresenceBeforeBusy != nil {
		t.Fatalf("owner after end %+v", u)
	}
}

func TestCreateRejectsSecondActiveCall(t *testing.T) {
	e := newStoreEnv(t)
	e.createOutbound(t)
	before := e.ownerUser(t)

	_, err := e.store.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Grace Hopper", "+12025550100")
	if !errors.Is(err, call.ErrActiveCallExists) {
		t.Fatalf("err = %v, want ErrActiveCallExists", err)
	}
	if n := e.countCalls(t); n != 1 {
		t.Fatalf("calls = %d", n)
	}
	after := e.ownerUser(t)
	if after.Version != before.Version || after.PresenceBeforeBusy == nil || *after.PresenceBeforeBusy != user.PresenceAvailable {
		t.Fatalf("owner changed: before %+v after %+v", before, after)
	}
}

func TestApplyAnswerInboundActivates(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createInbound(t)
	e.advance(7 * time.Second)

	got, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionAnswer, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != call.StatusActive || got.Version != 2 {
		t.Fatalf("call %+v", got)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(e.clock) {
		t.Fatalf("startedAt %v", got.StartedAt)
	}

	stored, ts, _, err := e.store.Get(e.ctx, e.org, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != call.StatusActive || stored.Version != 2 || stored.StartedAt == nil {
		t.Fatalf("stored %+v", stored)
	}
	if len(ts) != 2 {
		t.Fatalf("transitions %+v", ts)
	}
	last := ts[1]
	if last.From == nil || *last.From != call.StatusRinging || last.To != call.StatusActive || !last.OccurredAt.Equal(e.clock) {
		t.Fatalf("last transition %+v", last)
	}
	if u := e.ownerUser(t); u.Presence != user.PresenceBusy {
		t.Fatalf("owner must stay busy while active: %+v", u)
	}
}

func TestApplyStaleVersionLeavesRowUnchanged(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createInbound(t)

	_, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionAnswer, ExpectedVersion: 9})
	if !errors.Is(err, call.ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
	stored, ts, _, err := e.store.Get(e.ctx, e.org, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != call.StatusRinging || stored.Version != 1 || stored.StartedAt != nil || len(ts) != 1 {
		t.Fatalf("row changed: %+v %+v", stored, ts)
	}
}

func TestApplyInvalidTransitionAndUnknownCall(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createOutbound(t)

	_, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionAnswer, ExpectedVersion: 1})
	if !errors.Is(err, call.ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
	_, err = e.store.Apply(e.ctx, e.org, uuid.New(), call.Command{Action: call.ActionEnd, ExpectedVersion: 1})
	if !errors.Is(err, call.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	_, err = e.store.Apply(e.ctx, uuid.New(), c.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1})
	if !errors.Is(err, call.ErrNotFound) {
		t.Fatalf("other org: err = %v, want ErrNotFound", err)
	}
	if _, _, _, err := e.store.Get(e.ctx, uuid.New(), c.ID); !errors.Is(err, call.ErrNotFound) {
		t.Fatalf("Get other org: err = %v, want ErrNotFound", err)
	}
}

func TestApplyTerminalRestoresPresenceAndFreesOwner(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createInbound(t)
	e.advance(3 * time.Second)

	got, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionReject, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != call.StatusRejected || got.EndedAt == nil || !got.EndedAt.Equal(e.clock) {
		t.Fatalf("call %+v", got)
	}
	u := e.ownerUser(t)
	if u.Presence != user.PresenceAvailable || u.PresenceBeforeBusy != nil || u.Version != 3 {
		t.Fatalf("owner %+v", u)
	}

	has, err := e.store.HasNonTerminal(e.ctx, e.org, e.owner)
	if err != nil || has {
		t.Fatalf("HasNonTerminal = %v, %v", has, err)
	}
	// The owner may start another call now.
	e.createOutbound(t)
}

func TestApplyFailRecordsReason(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createOutbound(t)
	r := call.ReasonNoAnswer

	got, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionFail, ExpectedVersion: 1, Reason: &r})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != call.StatusFailed || got.FailureReason == nil || *got.FailureReason != r {
		t.Fatalf("call %+v", got)
	}
	_, ts, _, err := e.store.Get(e.ctx, e.org, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[1].Reason == nil || *ts[1].Reason != r {
		t.Fatalf("transitions %+v", ts)
	}
}

func TestHasNonTerminal(t *testing.T) {
	e := newStoreEnv(t)
	has, err := e.store.HasNonTerminal(e.ctx, e.org, e.owner)
	if err != nil || has {
		t.Fatalf("before: %v, %v", has, err)
	}
	e.createInbound(t)
	has, err = e.store.HasNonTerminal(e.ctx, e.org, e.owner)
	if err != nil || !has {
		t.Fatalf("after: %v, %v", has, err)
	}
	has, err = e.store.HasNonTerminal(e.ctx, uuid.New(), e.owner)
	if err != nil || has {
		t.Fatalf("other org: %v, %v", has, err)
	}
}

func TestSweepInterruptedFailsLeftoverCallsAndRestoresPresence(t *testing.T) {
	e := newStoreEnv(t)
	c := e.createInbound(t)
	e.advance(time.Minute)

	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
	stored, ts, _, err := e.store.Get(e.ctx, e.org, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != call.StatusFailed || stored.Version != 2 {
		t.Fatalf("stored %+v", stored)
	}
	if stored.FailureReason == nil || *stored.FailureReason != call.ReasonSimulationInterrupted {
		t.Fatalf("reason %v", stored.FailureReason)
	}
	if stored.EndedAt == nil || !stored.EndedAt.Equal(e.clock) {
		t.Fatalf("endedAt %v", stored.EndedAt)
	}
	if len(ts) != 2 {
		t.Fatalf("transitions %+v", ts)
	}
	last := ts[1]
	if last.From == nil || *last.From != call.StatusRinging || last.To != call.StatusFailed ||
		last.Reason == nil || *last.Reason != call.ReasonSimulationInterrupted {
		t.Fatalf("last transition %+v", last)
	}
	u := e.ownerUser(t)
	if u.Presence != user.PresenceAvailable || u.PresenceBeforeBusy != nil {
		t.Fatalf("owner %+v", u)
	}
}

func TestSweepInterruptedIgnoresTerminalCallsAndOtherOrgs(t *testing.T) {
	e := newStoreEnv(t)
	done := e.createOutbound(t)
	if _, err := e.store.Apply(e.ctx, e.org, done.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	live := e.createInbound(t)

	if err := e.store.SweepInterrupted(e.ctx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	stored, _, _, err := e.store.Get(e.ctx, e.org, live.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != call.StatusRinging {
		t.Fatalf("other org sweep touched call: %+v", stored)
	}

	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
	ended, _, _, err := e.store.Get(e.ctx, e.org, done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Status != call.StatusEnded || ended.Version != 2 || ended.FailureReason != nil {
		t.Fatalf("terminal call changed: %+v", ended)
	}
	// Sweeping again is a no-op.
	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
}
