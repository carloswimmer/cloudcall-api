package event_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cloudcall/internal/event"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// sseEvent is one parsed "id/event/data" block of the stream.
type sseEvent struct {
	id, name string
	env      event.Envelope
}

// readEvents parses the stream on a goroutine so a test can wait with a timeout.
func readEvents(t *testing.T, resp *http.Response) <-chan sseEvent {
	t.Helper()
	ch := make(chan sseEvent, 16)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.env); err != nil {
					t.Errorf("bad data line %q: %v", line, err)
				}
			case line == "":
				ch <- cur
				cur = sseEvent{}
			}
		}
	}()
	return ch
}

func nextEvent(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("stream ended, expected an event")
		}
		return e
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for an SSE event")
	}
	return sseEvent{}
}

func newSSEServer(t *testing.T, hub *event.Hub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	event.NewHandler(hub).Register(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	return srv
}

func TestStreamSendsSnapshotThenPublishedEvent(t *testing.T) {
	callID := uuid.New()
	hub := event.NewHub(&fakeLoader{snap: event.Snapshot{
		Calls: []any{map[string]any{"id": callID, "status": "dialing", "version": 1}},
	}}, time.Hour, time.Now)
	defer hub.Close()
	srv := newSSEServer(t, hub)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
	req.Header.Set("Origin", "http://localhost:4200")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if o := resp.Header.Get("Access-Control-Allow-Origin"); o != "http://localhost:4200" {
		t.Errorf("CORS origin = %q", o)
	}

	events := readEvents(t, resp)
	first := nextEvent(t, events)
	if first.name != "snapshot" || first.env.Type != "snapshot" || first.id == "" || first.id != first.env.EventID {
		t.Fatalf("first event = %+v, want snapshot", first)
	}
	var snap snapshotPayload
	if err := json.Unmarshal(first.env.Payload, &snap); err != nil || len(snap.Calls) != 1 {
		t.Fatalf("snapshot payload = %s (%v)", first.env.Payload, err)
	}

	hub.Publish(callEvent(callID, 2, "active"))
	second := nextEvent(t, events)
	if second.name != "call.updated" || second.env.EntityVersion == nil || *second.env.EntityVersion != 2 {
		t.Fatalf("second event = %+v, want call.updated v2", second)
	}
}

func TestStreamEndsWhenClientDisconnects(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	defer hub.Close()
	srv := newSSEServer(t, hub)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	events := readEvents(t, resp)
	nextEvent(t, events)
	cancel()
	resp.Body.Close()

	// The handler must unsubscribe once the request context is cancelled.
	deadline := time.Now().Add(waitTimeout)
	for hub.Clients() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("hub still has %d clients after the client disconnected", hub.Clients())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStreamEndsWhenHubCloses(t *testing.T) {
	hub := event.NewHub(&fakeLoader{}, time.Hour, time.Now)
	srv := newSSEServer(t, hub)

	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := readEvents(t, resp)
	nextEvent(t, events)

	hub.Close()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("unexpected event after hub close")
		}
	case <-time.After(waitTimeout):
		t.Fatal("stream still open after hub close")
	}
}

func TestStreamReportsSnapshotFailureAsJSONError(t *testing.T) {
	hub := event.NewHub(&fakeLoader{err: errors.New("db down")}, time.Hour, time.Now)
	defer hub.Close()
	srv := newSSEServer(t, hub)

	resp, err := http.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var body httpx.ErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "internal_error" || strings.Contains(body.Message, "db down") {
		t.Fatalf("body = %+v", body)
	}
}
