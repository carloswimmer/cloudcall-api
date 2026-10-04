package user

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// Handler serves identity, colleague and presence routes for the demo user.
// The client never supplies organization or user IDs; the seeded ones are used.
type Handler struct {
	store  *Store
	orgID  uuid.UUID
	userID uuid.UUID
	calls  CallLock
}

// CallLock tells whether a user has a dialing, ringing or active call. It is
// satisfied by *call.Store; the interface avoids an import cycle (call imports user).
type CallLock interface {
	HasNonTerminal(ctx context.Context, orgID, userID uuid.UUID) (bool, error)
}

// WithCallLock makes PATCH /me/presence answer 409 presence_locked during a live call.
func (h *Handler) WithCallLock(calls CallLock) *Handler {
	h.calls = calls
	return h
}

func NewHandler(store *Store, orgID, userID uuid.UUID) *Handler {
	return &Handler{store: store, orgID: orgID, userID: userID}
}

// Register mounts the user routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me", h.getMe)
	mux.HandleFunc("GET /api/v1/users", h.listUsers)
	mux.HandleFunc("PATCH /api/v1/me/presence", h.patchPresence)
}

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) {
	u, err := h.store.Get(r.Context(), h.orgID, h.userID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	org, err := h.store.Organization(r.Context(), h.orgID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":         u,
		"organization": org,
		"demoMode":     true,
	})
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.List(r.Context(), h.orgID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": users})
}

type presenceRequest struct {
	Presence        Presence `json:"presence"`
	ExpectedVersion int      `json:"expectedVersion"`
}

func (h *Handler) patchPresence(w http.ResponseWriter, r *http.Request) {
	var req presenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request body", nil)
		return
	}
	fields := map[string]string{}
	if !req.Presence.Valid() {
		fields["presence"] = "must be one of available, busy, offline"
	}
	if req.ExpectedVersion < 1 {
		fields["expectedVersion"] = "must be a positive integer"
	}
	if len(fields) > 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request", fields)
		return
	}

	if h.calls != nil {
		locked, err := h.calls.HasNonTerminal(r.Context(), h.orgID, h.userID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if locked {
			httpx.WriteError(w, r, http.StatusConflict, "presence_locked", "presence cannot be changed during a call", nil)
			return
		}
	}
	u, err := h.store.SetPresence(r.Context(), h.orgID, h.userID, req.Presence, req.ExpectedVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrVersionMismatch):
		httpx.WriteError(w, r, http.StatusConflict, "version_mismatch", "presence version is stale", nil)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	default:
		slog.Error("user handler", "requestId", httpx.RequestID(r), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
