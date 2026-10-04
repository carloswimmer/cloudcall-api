package call

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"cloudcall/internal/contact"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgForeignKeyViolation is the PostgreSQL SQLSTATE for foreign_key_violation.
const pgForeignKeyViolation = "23503"

// unknownPeerName is the snapshot name used when a call is placed by phone only.
const unknownPeerName = "Unknown"

// ContactFinder loads a contact of an organization. It is satisfied by *contact.Store.
type ContactFinder interface {
	Get(ctx context.Context, orgID, id uuid.UUID) (contact.Contact, error)
}

// Handler serves call commands and the demo simulator for the seeded organization
// and demo user. The client never supplies organization or user IDs.
type Handler struct {
	store    *Store
	contacts ContactFinder
	orgID    uuid.UUID
	ownerID  uuid.UUID
}

func NewHandler(store *Store, contacts ContactFinder, orgID, ownerID uuid.UUID) *Handler {
	return &Handler{store: store, contacts: contacts, orgID: orgID, ownerID: ownerID}
}

// Register mounts the user-facing call routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/calls", h.create)
	mux.HandleFunc("POST /api/v1/calls/{id}/actions", h.action)
}

// RegisterDemo mounts the simulator routes. The caller decides (config.DemoMode)
// whether to call it, so a deployment without demo mode never exposes them.
func (h *Handler) RegisterDemo(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/demo/incoming-call", h.incoming)
	mux.HandleFunc("POST /api/v1/demo/calls/{id}/connect", h.connect)
	mux.HandleFunc("POST /api/v1/demo/calls/{id}/fail", h.failCall)
}

// callResponse is the JSON shape of a call. Optional values are null when unset.
type callResponse struct {
	ID            uuid.UUID  `json:"id"`
	ContactID     *uuid.UUID `json:"contactId"`
	PeerName      string     `json:"peerName"`
	PeerPhone     string     `json:"peerPhone"`
	Direction     Direction  `json:"direction"`
	Status        Status     `json:"status"`
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt"`
	FailureReason *Reason    `json:"failureReason"`
}

func toResponse(c Call) callResponse {
	return callResponse{
		ID:            c.ID,
		ContactID:     c.ContactID,
		PeerName:      c.PeerNameSnapshot,
		PeerPhone:     c.PeerPhoneSnapshot,
		Direction:     c.Direction,
		Status:        c.Status,
		Version:       c.Version,
		CreatedAt:     c.CreatedAt,
		StartedAt:     c.StartedAt,
		EndedAt:       c.EndedAt,
		FailureReason: c.FailureReason,
	}
}

type createRequest struct {
	ContactID *string `json:"contactId"`
	Phone     *string `json:"phone"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, DirectionOutbound)
}

func (h *Handler) incoming(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, DirectionInbound)
}

// start validates the body, resolves the peer and creates a call in the given direction.
func (h *Handler) start(w http.ResponseWriter, r *http.Request, direction Direction) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, r, "invalid request body", nil)
		return
	}
	hasContact, hasPhone := req.ContactID != nil, req.Phone != nil
	if hasContact == hasPhone {
		badRequest(w, r, "send exactly one of contactId or phone", map[string]string{
			"contactId": "exactly one of contactId or phone is required",
			"phone":     "exactly one of contactId or phone is required",
		})
		return
	}

	var (
		contactID *uuid.UUID
		peerName  string
		peerPhone string
	)
	if hasContact {
		id, err := uuid.Parse(*req.ContactID)
		if err != nil {
			badRequest(w, r, "invalid request", map[string]string{"contactId": "must be a UUID"})
			return
		}
		// The contact must exist in this organization; checking first keeps a raw
		// foreign key error from reaching the client.
		c, err := h.contacts.Get(r.Context(), h.orgID, id)
		if errors.Is(err, contact.ErrNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "contact not found", nil)
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		contactID, peerName, peerPhone = &c.ID, c.Name, c.Phone
	} else {
		if err := contact.ValidatePhone(*req.Phone); err != nil {
			badRequest(w, r, "invalid request", map[string]string{"phone": err.Error()})
			return
		}
		peerName, peerPhone = unknownPeerName, *req.Phone
	}

	c, err := h.store.Create(r.Context(), h.orgID, h.ownerID, direction, contactID, peerName, peerPhone)
	if err != nil {
		// The contact was deleted between the lookup and the insert.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation {
			httpx.WriteError(w, r, http.StatusNotFound, "not_found", "contact not found", nil)
			return
		}
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toResponse(c))
}

type actionRequest struct {
	Action          Action `json:"action"`
	ExpectedVersion int    `json:"expectedVersion"`
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, r, "invalid request body", nil)
		return
	}
	fields := map[string]string{}
	switch req.Action {
	case ActionAnswer, ActionReject, ActionEnd:
	default:
		fields["action"] = "must be one of answer, reject, end"
	}
	if req.ExpectedVersion < 1 {
		fields["expectedVersion"] = "must be a positive integer"
	}
	if len(fields) > 0 {
		badRequest(w, r, "invalid request", fields)
		return
	}
	h.apply(w, r, id, Command{Action: req.Action, ExpectedVersion: req.ExpectedVersion})
}

type versionRequest struct {
	ExpectedVersion int `json:"expectedVersion"`
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req versionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, r, "invalid request body", nil)
		return
	}
	if req.ExpectedVersion < 1 {
		badRequest(w, r, "invalid request", map[string]string{"expectedVersion": "must be a positive integer"})
		return
	}
	h.apply(w, r, id, Command{Action: ActionConnect, ExpectedVersion: req.ExpectedVersion})
}

type failRequest struct {
	ExpectedVersion int    `json:"expectedVersion"`
	Reason          Reason `json:"reason"`
}

func (h *Handler) failCall(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req failRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, r, "invalid request body", nil)
		return
	}
	fields := map[string]string{}
	if req.ExpectedVersion < 1 {
		fields["expectedVersion"] = "must be a positive integer"
	}
	switch req.Reason {
	case ReasonNoAnswer, ReasonRingTimeout, ReasonNetworkError:
	default:
		fields["reason"] = "must be one of no_answer, ring_timeout, network_error"
	}
	if len(fields) > 0 {
		badRequest(w, r, "invalid request", fields)
		return
	}
	reason := req.Reason
	h.apply(w, r, id, Command{Action: ActionFail, ExpectedVersion: req.ExpectedVersion, Reason: &reason})
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request, id uuid.UUID, cmd Command) {
	c, err := h.store.Apply(r.Context(), h.orgID, id, cmd)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(c))
}

// pathID parses the {id} segment. A malformed ID can never match a call, so it is a 404.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "call not found", nil)
		return uuid.UUID{}, false
	}
	return id, true
}

func badRequest(w http.ResponseWriter, r *http.Request, message string, fields map[string]string) {
	httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", message, fields)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "call not found", nil)
	case errors.Is(err, ErrActiveCallExists):
		httpx.WriteError(w, r, http.StatusConflict, "active_call_exists", "a call is already in progress", nil)
	case errors.Is(err, ErrVersionMismatch):
		httpx.WriteError(w, r, http.StatusConflict, "version_mismatch", "call version is stale", nil)
	case errors.Is(err, ErrInvalidTransition):
		httpx.WriteError(w, r, http.StatusConflict, "invalid_transition", "action is not allowed in the current call state", nil)
	case errors.Is(err, ErrValidation):
		badRequest(w, r, "invalid request", map[string]string{"reason": "is not valid for this call"})
	default:
		slog.Error("call handler", "requestId", httpx.RequestID(r), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
