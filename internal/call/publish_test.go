package call_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/event"
	"cloudcall/internal/user"
)

type recorder struct {
	mu     sync.Mutex
	events []event.Envelope
}

func (r *recorder) Publish(e event.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) take() []event.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

// newPublishingEnv wires one recorder into both stores, as app.New does.
func newPublishingEnv(t *testing.T) (*storeEnv, *recorder) {
	t.Helper()
	e := newStoreEnv(t)
	rec := &recorder{}
	e.store.Publisher = rec
	e.users.Publisher = rec
	return e, rec
}

func ofType(events []event.Envelope, typ string) []event.Envelope {
	var out []event.Envelope
	for _, ev := range events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func TestCreatePublishesCallAndPresenceAfterCommit(t *testing.T) {
	e, rec := newPublishingEnv(t)
	c := e.createOutbound(t)

	events := rec.take()
	calls, presence := ofType(events, "call.updated"), ofType(events, "presence.updated")
	if len(calls) != 1 || len(presence) != 1 || len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}

	ev := calls[0]
	if ev.EntityID == nil || *ev.EntityID != c.ID.String() || ev.EntityVersion == nil || *ev.EntityVersion != 1 {
		t.Fatalf("call event entity = %+v", ev)
	}
	var body map[string]any
	if err := json.Unmarshal(ev.Payload, &body); err != nil {
		t.Fatal(err)
	}
	// The payload is the whole call, as the REST API returns it.
	for _, key := range []string{"id", "contactId", "peerName", "peerPhone", "direction", "status", "version", "createdAt", "startedAt", "endedAt", "failureReason"} {
		if _, ok := body[key]; !ok {
			t.Errorf("payload misses %q: %s", key, ev.Payload)
		}
	}
	if body["status"] != "dialing" || body["peerName"] != "Ada Lovelace" {
		t.Errorf("payload = %s", ev.Payload)
	}

	var u user.User
	if err := json.Unmarshal(presence[0].Payload, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != e.owner || u.Presence != user.PresenceBusy || u.Version != 2 {
		t.Fatalf("presence payload = %+v", u)
	}
	if presence[0].EntityVersion == nil || *presence[0].EntityVersion != 2 {
		t.Fatalf("presence entityVersion = %v", presence[0].EntityVersion)
	}
}

func TestCreateRejectedPublishesNothing(t *testing.T) {
	e, rec := newPublishingEnv(t)
	e.createOutbound(t)
	rec.take()

	_, err := e.store.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Grace Hopper", "+12025550100")
	if !errors.Is(err, call.ErrActiveCallExists) {
		t.Fatalf("err = %v", err)
	}
	if events := rec.take(); len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestApplyPublishesCallAndRestoredPresence(t *testing.T) {
	e, rec := newPublishingEnv(t)
	c := e.createInbound(t)
	rec.take()

	if _, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionAnswer, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	events := rec.take()
	if calls := ofType(events, "call.updated"); len(calls) != 1 || *calls[0].EntityVersion != 2 {
		t.Fatalf("answer events = %+v", events)
	}
	if p := ofType(events, "presence.updated"); len(p) != 0 {
		t.Fatalf("answering must not change presence: %+v", p)
	}

	e.advance(time.Minute)
	if _, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	events = rec.take()
	calls, presence := ofType(events, "call.updated"), ofType(events, "presence.updated")
	if len(calls) != 1 || len(presence) != 1 {
		t.Fatalf("end events = %+v", events)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(calls[0].Payload, &body); err != nil || body.Status != "ended" || *calls[0].EntityVersion != 3 {
		t.Fatalf("end call event = %s (%v)", calls[0].Payload, err)
	}
	var u user.User
	if err := json.Unmarshal(presence[0].Payload, &u); err != nil {
		t.Fatal(err)
	}
	if u.Presence != user.PresenceAvailable || u.Version != 3 {
		t.Fatalf("restored presence = %+v", u)
	}
}

func TestApplyFailurePublishesNothing(t *testing.T) {
	e, rec := newPublishingEnv(t)
	c := e.createInbound(t)
	rec.take()

	if _, err := e.store.Apply(e.ctx, e.org, c.ID, call.Command{Action: call.ActionAnswer, ExpectedVersion: 9}); !errors.Is(err, call.ErrVersionMismatch) {
		t.Fatalf("err = %v", err)
	}
	if events := rec.take(); len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestSweepInterruptedPublishesEachFailedCallAndOwnerPresence(t *testing.T) {
	e, rec := newPublishingEnv(t)
	c := e.createInbound(t)
	rec.take()

	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
	events := rec.take()
	calls, presence := ofType(events, "call.updated"), ofType(events, "presence.updated")
	if len(calls) != 1 || len(presence) != 1 {
		t.Fatalf("events = %+v", events)
	}
	if *calls[0].EntityID != c.ID.String() || *calls[0].EntityVersion != 2 {
		t.Fatalf("call event = %+v", calls[0])
	}
	var body struct {
		Status        string  `json:"status"`
		FailureReason *string `json:"failureReason"`
	}
	if err := json.Unmarshal(calls[0].Payload, &body); err != nil || body.Status != "failed" ||
		body.FailureReason == nil || *body.FailureReason != "simulation_interrupted" {
		t.Fatalf("payload = %s (%v)", calls[0].Payload, err)
	}
}

func TestSweepWithNothingToFailPublishesNothing(t *testing.T) {
	e, rec := newPublishingEnv(t)
	if err := e.store.SweepInterrupted(e.ctx, e.org); err != nil {
		t.Fatal(err)
	}
	if events := rec.take(); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
}

func TestActiveAndTombstoneQueries(t *testing.T) {
	e := newStoreEnv(t)
	ended := e.createOutbound(t)
	if _, err := e.store.Apply(e.ctx, e.org, ended.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Minute)
	live := e.createInbound(t)

	active, err := e.store.Active(e.ctx, e.org)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != live.ID {
		t.Fatalf("active = %+v", active)
	}

	since := e.clock.Add(-15 * time.Minute)
	tombs, err := e.store.RecentTerminal(e.ctx, e.org, e.owner, since, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 1 || tombs[0].ID != ended.ID || tombs[0].Status != call.StatusEnded || tombs[0].Version != 2 {
		t.Fatalf("tombstones = %+v", tombs)
	}

	// Outside the window, or over the limit, nothing comes back.
	none, err := e.store.RecentTerminal(e.ctx, e.org, e.owner, e.clock.Add(time.Hour), 100)
	if err != nil || len(none) != 0 {
		t.Fatalf("future since = %+v (%v)", none, err)
	}
	none, err = e.store.RecentTerminal(e.ctx, e.org, e.owner, since, 0)
	if err != nil || len(none) != 0 {
		t.Fatalf("limit 0 = %+v (%v)", none, err)
	}
}
