package call

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"cloudcall/internal/contact"
	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgForeignKeyViolation is the PostgreSQL SQLSTATE for foreign_key_violation.
const pgForeignKeyViolation = "23503"

// maxNoteLen is the longest note text, counted in characters (it matches the
// char_length check on call_notes).
const maxNoteLen = 2000

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
	mux.HandleFunc("GET /api/v1/calls", h.list)
	mux.HandleFunc("POST /api/v1/calls", h.create)
	mux.HandleFunc("GET /api/v1/calls/{id}", h.detail)
	mux.HandleFunc("PUT /api/v1/calls/{id}/note", h.putNote)
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
	// DurationSeconds is present only when the call has both startedAt and endedAt.
	DurationSeconds *int64 `json:"durationSeconds,omitempty"`
}

// Payload is the JSON-ready shape of a call, shared by the REST responses and
// the SSE snapshot and events so every channel shows the same fields.
func Payload(c Call) any { return toResponse(c) }

func toResponse(c Call) callResponse {
	var duration *int64
	if c.StartedAt != nil && c.EndedAt != nil {
		d := int64(c.EndedAt.Sub(*c.StartedAt) / time.Second)
		duration = &d
	}
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

		DurationSeconds: duration,
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

// transitionResponse is one entry of a call's timeline.
type transitionResponse struct {
	From       *Status   `json:"from"`
	To         Status    `json:"to"`
	OccurredAt time.Time `json:"occurredAt"`
	Reason     *Reason   `json:"reason"`
}

type noteResponse struct {
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toNoteResponse(n Note) noteResponse {
	return noteResponse{Text: n.Text, UpdatedAt: n.UpdatedAt}
}

// detailResponse is a call plus its timeline and note (null when there is none).
type detailResponse struct {
	callResponse
	Transitions []transitionResponse `json:"transitions"`
	Note        *noteResponse        `json:"note"`
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	c, transitions, note, err := h.store.Get(r.Context(), h.orgID, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := detailResponse{callResponse: toResponse(c), Transitions: make([]transitionResponse, 0, len(transitions))}
	for _, t := range transitions {
		out.Transitions = append(out.Transitions, transitionResponse{From: t.From, To: t.To, OccurredAt: t.OccurredAt, Reason: t.Reason})
	}
	if note != nil {
		n := toNoteResponse(*note)
		out.Note = &n
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type noteRequest struct {
	Text *string `json:"text"`
}

func (h *Handler) putNote(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req noteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, r, "invalid request body", nil)
		return
	}
	switch {
	case req.Text == nil:
		badRequest(w, r, "invalid request", map[string]string{"text": "is required"})
		return
	case utf8.RuneCountInString(*req.Text) > maxNoteLen:
		badRequest(w, r, "invalid request", map[string]string{"text": "must be at most 2000 characters"})
		return
	}
	note, err := h.store.SetNote(r.Context(), h.orgID, id, *req.Text)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toNoteResponse(note))
}

// list serves the paginated call history, newest first.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := map[string]string{}
	var f ListFilter

	if v := q.Get("direction"); v != "" {
		d := Direction(v)
		if d != DirectionInbound && d != DirectionOutbound {
			fields["direction"] = "must be one of inbound, outbound"
		} else {
			f.Direction = &d
		}
	}
	if v := q.Get("status"); v != "" {
		s := Status(v)
		switch s {
		case StatusDialing, StatusRinging, StatusActive, StatusEnded, StatusRejected, StatusMissed, StatusFailed:
			f.Status = &s
		default:
			fields["status"] = "must be one of dialing, ringing, active, ended, rejected, missed, failed"
		}
	}
	if v := q.Get("terminal"); v != "" {
		switch v {
		case "true", "false":
			b := v == "true"
			f.Terminal = &b
		default:
			fields["terminal"] = "must be true or false"
		}
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				fields[p.name] = "must be an RFC 3339 timestamp, for example 2026-10-04T00:00:00Z"
				continue
			}
			t = t.UTC()
			*p.dst = &t
		}
	}

	page, size, err := contact.ParsePage(q.Get("page"), q.Get("pageSize"))
	if err != nil {
		// Blame the parameter that is invalid on its own; otherwise the combination overflows.
		if _, _, perr := contact.ParsePage(q.Get("page"), ""); perr != nil {
			fields["page"] = perr.Error()
		} else if _, _, serr := contact.ParsePage("", q.Get("pageSize")); serr != nil {
			fields["pageSize"] = serr.Error()
		} else {
			fields["page"] = err.Error()
		}
	}
	if len(fields) > 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid query", fields)
		return
	}

	// ParsePage guarantees (page-1)*size fits in an int.
	calls, total, err := h.store.List(r.Context(), h.orgID, f, size, (page-1)*size)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]callResponse, 0, len(calls))
	for _, c := range calls {
		items = append(items, toResponse(c))
	}
	httpx.WriteJSON(w, http.StatusOK, contact.Page[callResponse]{Items: items, Total: total, Page: page, PageSize: size})
}
