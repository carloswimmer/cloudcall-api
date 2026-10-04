package event

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"cloudcall/internal/platform/httpx"
)

// writeTimeout bounds each event write. The server has no global WriteTimeout
// (it would kill the stream), so every write sets its own deadline.
const writeTimeout = 5 * time.Second

// Handler serves GET /api/v1/events. The client never sends organization or user
// IDs: the loader behind the hub decides whose state is streamed.
type Handler struct{ hub *Hub }

func NewHandler(hub *Hub) *Handler { return &Handler{hub: hub} }

// Register mounts the stream route on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", h.stream)
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	// Subscribe reads the snapshot before returning, so a failure is still
	// reported as a normal JSON error: headers go out with the first event only.
	_, out, cancel := h.hub.Subscribe(r.Context())
	defer cancel()

	rc := http.NewResponseController(w)
	started := false
	for {
		select {
		case env, ok := <-out:
			if !ok {
				if !started {
					slog.Error("event stream", "requestId", httpx.RequestID(r), "error", "subscribe failed or hub closed")
					httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
				}
				return
			}
			if !started {
				hd := w.Header()
				hd.Set("Content-Type", "text/event-stream")
				hd.Set("Cache-Control", "no-cache")
				started = true
			}
			data, err := json.Marshal(env)
			if err != nil {
				slog.Error("event stream marshal", "requestId", httpx.RequestID(r), "error", err)
				continue
			}
			// Not every ResponseWriter supports deadlines (httptest recorders do not).
			_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", env.EventID, env.Type, data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
