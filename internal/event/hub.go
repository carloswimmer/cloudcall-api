package event

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// QueueSize is the per-client buffer. A client whose queue is full is
// disconnected; it reconnects and receives a fresh snapshot.
const QueueSize = 32

// Hub fans events out to the connected SSE clients of this process.
type Hub struct {
	loader    SnapshotLoader
	heartbeat time.Duration
	now       func() time.Time

	mu      sync.Mutex
	clients map[string]*client
	closed  bool
	wg      sync.WaitGroup // one entry per running pump goroutine
}

type client struct {
	queue chan Envelope // written by Publish, read by the pump
	done  chan struct{} // closed once, when the client must stop
	once  sync.Once
}

func (c *client) stop() { c.once.Do(func() { close(c.done) }) }

// NewHub creates a hub. heartbeat <= 0 disables heartbeats. now may be nil.
func NewHub(loader SnapshotLoader, heartbeat time.Duration, now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	return &Hub{
		loader:    loader,
		heartbeat: heartbeat,
		now:       now,
		clients:   map[string]*client{},
	}
}

// Publish queues e for every client without ever blocking. A client whose queue
// is full is disconnected and removed; the others are unaffected.
func (h *Hub) Publish(e Envelope) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = h.now().UTC()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.clients {
		select {
		case c.queue <- e:
		default:
			c.stop()
			delete(h.clients, id)
		}
	}
}

// Subscribe registers a client, reads the snapshot and then streams events.
//
// Order matters: the client is registered first, so anything published while
// the snapshot is being read is queued and delivered right after it. The
// returned channel is closed when the client is disconnected, the context is
// cancelled, cancel is called, the hub is closed or the snapshot cannot be
// loaded (in that case no event is ever sent).
func (h *Hub) Subscribe(ctx context.Context) (string, <-chan Envelope, func()) {
	id := uuid.NewString()
	c := &client{queue: make(chan Envelope, QueueSize), done: make(chan struct{})}
	out := make(chan Envelope)
	cancel := func() { h.remove(id, c) }

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(out)
		return id, out, func() {}
	}
	h.clients[id] = c
	h.wg.Add(1)
	h.mu.Unlock()

	snap, err := h.load(ctx)
	if err != nil || ctx.Err() != nil || isDone(c) {
		cancel()
		h.wg.Done()
		close(out)
		return id, out, cancel
	}

	// Events queued so far are delivered after the snapshot; their call IDs also
	// become tombstones, so the snapshot itself already protects those calls.
	pending := drain(c.queue)
	snapshot := h.snapshotEnvelope(snap, pending)

	go h.pump(ctx, id, c, out, snapshot, pending)
	return id, out, cancel
}

func (h *Hub) load(ctx context.Context) (Snapshot, error) {
	if h.loader == nil {
		return Snapshot{}, nil
	}
	return h.loader.LoadSnapshot(ctx)
}

func isDone(c *client) bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func drain(q chan Envelope) []Envelope {
	var out []Envelope
	for {
		select {
		case e := <-q:
			out = append(out, e)
		default:
			return out
		}
	}
}

// pump owns the client's output channel: snapshot, queued events, then live
// events and heartbeats, until the client is stopped.
func (h *Hub) pump(ctx context.Context, id string, c *client, out chan<- Envelope, snapshot Envelope, pending []Envelope) {
	defer h.wg.Done()
	defer close(out)
	defer h.remove(id, c)

	send := func(e Envelope) bool {
		select {
		case out <- e:
			return true
		case <-c.done:
			return false
		case <-ctx.Done():
			return false
		}
	}

	if !send(snapshot) {
		return
	}
	for _, e := range pending {
		if !send(e) {
			return
		}
	}

	var tick <-chan time.Time
	if h.heartbeat > 0 {
		t := time.NewTicker(h.heartbeat)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case e := <-c.queue:
			if !send(e) {
				return
			}
		case <-tick:
			if !send(Envelope{
				EventID:    uuid.NewString(),
				Type:       TypeHeartbeat,
				OccurredAt: h.now().UTC(),
				Payload:    json.RawMessage(`{}`),
			}) {
				return
			}
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Clients returns how many clients are currently registered.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) remove(id string, c *client) {
	h.mu.Lock()
	if h.clients[id] == c {
		delete(h.clients, id)
	}
	h.mu.Unlock()
	c.stop()
}

// Close disconnects every client, stops their goroutines (heartbeats included)
// and waits for them. Publish and Subscribe stay safe to call afterwards.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	for id, c := range h.clients {
		c.stop()
		delete(h.clients, id)
	}
	h.mu.Unlock()
	h.wg.Wait()
}

type snapshotBody struct {
	Calls      []any       `json:"calls"`
	Users      []any       `json:"users"`
	Tombstones []Tombstone `json:"tombstones"`
}

// snapshotEnvelope builds the snapshot event. Tombstones from the loader are
// merged with every call.updated already queued for this client, keeping the
// highest version per call ID.
func (h *Hub) snapshotEnvelope(s Snapshot, queued []Envelope) Envelope {
	body := snapshotBody{Calls: s.Calls, Users: s.Users, Tombstones: mergeTombstones(s.Tombstones, queued)}
	if body.Calls == nil {
		body.Calls = []any{}
	}
	if body.Users == nil {
		body.Users = []any{}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		payload = []byte(`{"calls":[],"users":[],"tombstones":[]}`)
	}
	return Envelope{
		EventID:    uuid.NewString(),
		Type:       TypeSnapshot,
		OccurredAt: h.now().UTC(),
		Payload:    payload,
	}
}

func mergeTombstones(loaded []Tombstone, queued []Envelope) []Tombstone {
	out := make([]Tombstone, 0, len(loaded))
	index := map[uuid.UUID]int{}
	for _, t := range loaded {
		index[t.ID] = len(out)
		out = append(out, t)
	}
	for _, e := range queued {
		if e.Type != TypeCallUpdated || e.EntityID == nil || e.EntityVersion == nil {
			continue
		}
		id, err := uuid.Parse(*e.EntityID)
		if err != nil {
			continue
		}
		var p struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		t := Tombstone{ID: id, Version: *e.EntityVersion, Status: p.Status}
		if i, ok := index[id]; ok {
			if t.Version > out[i].Version {
				out[i] = t
			}
			continue
		}
		index[id] = len(out)
		out = append(out, t)
	}
	return out
}
