package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"cloudcall/internal/event"

	"github.com/google/uuid"
)

const waitTimeout = 2 * time.Second

// fakeLoader returns a fixed snapshot. onLoad runs inside LoadSnapshot, which
// lets a test publish "while the snapshot is being read".
type fakeLoader struct {
	snap   event.Snapshot
	err    error
	onLoad func()
}

func (f *fakeLoader) LoadSnapshot(context.Context) (event.Snapshot, error) {
	if f.onLoad != nil {
		f.onLoad()
	}
	return f.snap, f.err
}

func callEvent(id uuid.UUID, version int, status string) event.Envelope {
	env, err := event.NewEnvelope("call.updated", id, version, map[string]any{"id": id, "status": status, "version": version})
	if err != nil {
		panic(err)
	}
	return env
}

func recv(t *testing.T, out <-chan event.Envelope) event.Envelope {
	t.Helper()
	select {
	case e, ok := <-out:
		if !ok {
			t.Fatal("channel closed, expected an event")
		}
		return e
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for an event")
	}
	return event.Envelope{}
}

func expectClosed(t *testing.T, out <-chan event.Envelope) {
	t.Helper()
	deadline := time.After(waitTimeout)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the channel to close")
		}
	}
}

type snapshotPayload struct {
	Calls      []map[string]any  `json:"calls"`
	Users      []map[string]any  `json:"users"`
	Tombstones []event.Tombstone `json:"tombstones"`
}

func decodeSnapshot(t *testing.T, e event.Envelope) snapshotPayload {
	t.Helper()
	if e.Type != "snapshot" {
		t.Fatalf("type = %q, want snapshot", e.Type)
	}
	if e.EntityVersion != nil || e.EntityID != nil {
		t.Fatalf("snapshot must carry no entity id/version: %+v", e)
	}
	var p snapshotPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSubscribeSendsSnapshotThenQueuedEventsInOrder(t *testing.T) {
	callID := uuid.New()
	loader := &fakeLoader{snap: event.Snapshot{
		Calls: []any{map[string]any{"id": callID, "status": "dialing", "version": 1}},
		Users: []any{map[string]any{"name": "Demo"}},
	}}
	hub := event.NewHub(loader, time.Hour, time.Now)
	defer hub.Close()
	// Published after the client is registered but before the snapshot is sent.
	loader.onLoad = func() {
		hub.Publish(callEvent(callID, 2, "active"))
		hub.Publish(callEvent(callID, 3, "ended"))
	}

	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	snap := decodeSnapshot(t, recv(t, out))
	if len(snap.Calls) != 1 || len(snap.Users) != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if e := recv(t, out); e.Type != "call.updated" || *e.EntityVersion != 2 {
		t.Fatalf("second event = %+v, want call.updated v2", e)
	}
	if e := recv(t, out); e.Type != "call.updated" || *e.EntityVersion != 3 {
		t.Fatalf("third event = %+v, want call.updated v3", e)
	}

	hub.Publish(callEvent(callID, 4, "ended"))
	if e := recv(t, out); *e.EntityVersion != 4 {
		t.Fatalf("live event = %+v", e)
	}
}

func TestSnapshotUsesEmptyArraysNotNull(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	defer hub.Close()
	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	e := recv(t, out)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"calls", "users", "tombstones"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
}

func TestSnapshotTombstonesUnionQueuedCallIDs(t *testing.T) {
	endedID, queuedID := uuid.New(), uuid.New()
	loader := &fakeLoader{snap: event.Snapshot{
		Tombstones: []event.Tombstone{{ID: endedID, Version: 3, Status: "ended"}},
	}}
	hub := event.NewHub(loader, time.Hour, time.Now)
	defer hub.Close()
	loader.onLoad = func() {
		hub.Publish(callEvent(queuedID, 4, "failed")) // not in the database read yet
		hub.Publish(callEvent(endedID, 5, "ended"))   // newer than the loaded tombstone
		hub.Publish(event.Envelope{Type: "presence.updated", Payload: json.RawMessage(`{}`)})
	}

	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	snap := decodeSnapshot(t, recv(t, out))
	got := map[uuid.UUID]event.Tombstone{}
	for _, ts := range snap.Tombstones {
		got[ts.ID] = ts
	}
	if len(got) != 2 {
		t.Fatalf("tombstones = %+v, want exactly 2", snap.Tombstones)
	}
	if ts := got[queuedID]; ts.Version != 4 || ts.Status != "failed" {
		t.Errorf("queued tombstone = %+v", ts)
	}
	if ts := got[endedID]; ts.Version != 5 {
		t.Errorf("loaded tombstone must take the higher queued version, got %+v", ts)
	}
	// The queued events are still delivered after the snapshot.
	for i := 0; i < 3; i++ {
		recv(t, out)
	}
}

// A correct client never resurrects a terminal call: the snapshot carries the
// tombstone, an older call.updated still reaches the client (the hub does not
// filter) and Prefer tells the client to drop it.
func TestOlderEventAfterTombstoneIsDeliveredButNotPreferred(t *testing.T) {
	id := uuid.New()
	loader := &fakeLoader{snap: event.Snapshot{
		Tombstones: []event.Tombstone{{ID: id, Version: 3, Status: "ended"}},
	}}
	hub := event.NewHub(loader, time.Hour, time.Now)
	defer hub.Close()
	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	snap := decodeSnapshot(t, recv(t, out))
	floor := snap.Tombstones[0].Version

	hub.Publish(callEvent(id, 2, "active"))
	late := recv(t, out)
	if late.Type != "call.updated" || *late.EntityVersion != 2 {
		t.Fatalf("hub must still deliver the older event, got %+v", late)
	}
	if event.Prefer(floor, late) {
		t.Fatal("Prefer must reject an event older than the tombstone")
	}
}

func TestPrefer(t *testing.T) {
	v := func(n int) *int { return &n }
	tests := []struct {
		name     string
		existing int
		incoming event.Envelope
		want     bool
	}{
		{"older version is dropped", 3, event.Envelope{EntityVersion: v(2)}, false},
		{"same version is kept", 3, event.Envelope{EntityVersion: v(3)}, true},
		{"newer version wins", 3, event.Envelope{EntityVersion: v(4)}, true},
		{"unversioned envelope is kept", 3, event.Envelope{Type: "heartbeat"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := event.Prefer(tt.existing, tt.incoming); got != tt.want {
				t.Fatalf("Prefer(%d, ...) = %v, want %v", tt.existing, got, tt.want)
			}
		})
	}
}

func TestFullQueueDisconnectsOnlyTheSlowClient(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	defer hub.Close()

	_, slow, cancelSlow := hub.Subscribe(context.Background())
	defer cancelSlow()
	_, fast, cancelFast := hub.Subscribe(context.Background())
	defer cancelFast()
	if e := recv(t, fast); e.Type != "snapshot" {
		t.Fatalf("fast first event = %q", e.Type)
	}

	id := uuid.New()
	total := event.QueueSize*2 + 5
	for i := 1; i <= total; i++ {
		hub.Publish(callEvent(id, i, "active"))
		// The fast client keeps up, so its queue never fills.
		if e := recv(t, fast); *e.EntityVersion != i {
			t.Fatalf("fast client got version %d, want %d", *e.EntityVersion, i)
		}
	}

	// The slow client never read: its channel ends closed, without all events.
	received := 0
	deadline := time.After(waitTimeout)
loop:
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				break loop
			}
			received++
		case <-deadline:
			t.Fatal("slow client was not disconnected")
		}
	}
	if received > event.QueueSize+2 {
		t.Fatalf("slow client received %d events, more than the queue allows", received)
	}

	// The fast client is still connected.
	hub.Publish(callEvent(id, total+1, "active"))
	if e := recv(t, fast); *e.EntityVersion != total+1 {
		t.Fatalf("fast client lost after the slow one was dropped: %+v", e)
	}
}

func TestContextCancelStopsReceiving(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, out, stop := hub.Subscribe(ctx)
	defer stop()
	recv(t, out) // snapshot

	cancel()
	expectClosed(t, out)

	// Publishing after the cancel must not panic or block.
	hub.Publish(callEvent(uuid.New(), 1, "active"))
}

func TestCancelFuncIsIdempotent(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	defer hub.Close()
	_, out, cancel := hub.Subscribe(context.Background())
	cancel()
	cancel()
	expectClosed(t, out)
}

func TestSubscribeWithFailingLoaderClosesChannel(t *testing.T) {
	hub := event.NewHub(&fakeLoader{err: errors.New("db down")}, time.Hour, time.Now)
	defer hub.Close()
	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	select {
	case e, ok := <-out:
		if ok {
			t.Fatalf("unexpected event %+v", e)
		}
	case <-time.After(waitTimeout):
		t.Fatal("channel not closed after loader failure")
	}
}

func TestCloseCancelsEveryClient(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	var outs []<-chan event.Envelope
	for i := 0; i < 3; i++ {
		_, out, _ := hub.Subscribe(context.Background())
		outs = append(outs, out)
	}
	hub.Close()
	hub.Close() // idempotent
	for _, out := range outs {
		expectClosed(t, out)
	}

	hub.Publish(callEvent(uuid.New(), 1, "active")) // no panic
	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()
	expectClosed(t, out)
}

func TestHeartbeatIsSentPeriodically(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, 20*time.Millisecond, time.Now)
	defer hub.Close()
	_, out, cancel := hub.Subscribe(context.Background())
	defer cancel()

	recv(t, out) // snapshot
	for i := 0; i < 2; i++ {
		e := recv(t, out)
		if e.Type != "heartbeat" || e.EventID == "" || e.OccurredAt.IsZero() {
			t.Fatalf("heartbeat = %+v", e)
		}
	}
}

func TestEventIDsAreUniqueInTheProcess(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		e := callEvent(uuid.New(), 1, "active")
		if e.EventID == "" || seen[e.EventID] {
			t.Fatalf("duplicate or empty event id %q", e.EventID)
		}
		seen[e.EventID] = true
	}
}

func TestCloseDoesNotLeakGoroutines(t *testing.T) {
	cycle := func() {
		hub := event.NewHub(&fakeLoader{}, 5*time.Millisecond, time.Now)
		ctx, cancel := context.WithCancel(context.Background())
		_, out, stop := hub.Subscribe(ctx)
		<-out // snapshot
		time.Sleep(12 * time.Millisecond)
		hub.Close()
		stop()
		cancel()
	}
	cycle() // warm up runtime goroutines
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		cycle()
	}
	time.Sleep(100 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+3 {
		t.Fatalf("goroutines grew from %d to %d across 20 open/close cycles", before, after)
	}
}

func TestNopPublisher(t *testing.T) {
	var p event.Publisher = event.Nop{}
	p.Publish(event.Envelope{})
}
