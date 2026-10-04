package contact

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// Handler serves contact routes for the seeded organization. The client never
// supplies an organization ID.
type Handler struct {
	store *Store
	orgID uuid.UUID
}

func NewHandler(store *Store, orgID uuid.UUID) *Handler {
	return &Handler{store: store, orgID: orgID}
}

// Register mounts the contact routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/contacts", h.list)
	mux.HandleFunc("POST /api/v1/contacts", h.create)
	mux.HandleFunc("PATCH /api/v1/contacts/{id}", h.patch)
	mux.HandleFunc("DELETE /api/v1/contacts/{id}", h.delete)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	fields := map[string]string{}
	page, size, err := ParsePage(query.Get("page"), query.Get("pageSize"))
	if err != nil {
		// Report against the offending parameter.
		if p := query.Get("page"); p != "" {
			if _, _, perr := ParsePage(p, ""); perr != nil {
				fields["page"] = perr.Error()
			}
		}
		if s := query.Get("pageSize"); s != "" {
			if _, _, perr := ParsePage("", s); perr != nil {
				fields["pageSize"] = perr.Error()
			}
		}
		if len(fields) == 0 {
			if query.Get("page") != "" {
				fields["page"] = err.Error()
			} else if query.Get("pageSize") != "" {
				fields["pageSize"] = err.Error()
			}
		}
	}
	if sort := query.Get("sort"); sort != "" && sort != "name" {
		fields["sort"] = "must be name"
	}
	if len(fields) > 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid query", fields)
		return
	}

	res, err := h.store.List(r.Context(), h.orgID, query.Get("q"), page, size)
	if err != nil {
		slog.Error("contact handler", "requestId", httpx.RequestID(r), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// optionalString distinguishes an absent JSON field from an explicit null.
type optionalString struct {
	Set   bool
	Value *string
}

func (o *optionalString) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

type createRequest struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`
	Email *string `json:"email"`
}

type patchRequest struct {
	Name  *string        `json:"name"`
	Phone *string        `json:"phone"`
	Email optionalString `json:"email"`
}

// validateFields returns per-field messages for the values that are present.
// Name and phone are required columns, so an explicit null is rejected here.
func validateFields(name, phone, email *string, nameSent, phoneSent bool) map[string]string {
	fields := map[string]string{}
	if name != nil {
		if err := ValidateName(*name); err != nil {
			fields["name"] = err.Error()
		}
	} else if nameSent {
		fields["name"] = "is required"
	}
	if phone != nil {
		if err := ValidatePhone(*phone); err != nil {
			fields["phone"] = err.Error()
		}
	} else if phoneSent {
		fields["phone"] = "is required"
	}
	if email != nil {
		if err := ValidateEmail(*email); err != nil {
			fields["email"] = err.Error()
		}
	}
	return fields
}

// emptyToNil stores an empty email as no email.
func emptyToNil(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request body", nil)
		return
	}
	// On create, name and phone must be present.
	fields := validateFields(req.Name, req.Phone, req.Email, true, true)
	if len(fields) > 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request", fields)
		return
	}

	c, err := h.store.Create(r.Context(), h.orgID, NewContact{
		Name:  *trimmed(req.Name),
		Phone: *req.Phone,
		Email: emptyToNil(req.Email),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	body, err := io.ReadAll(r.Body)
	if err == nil {
		err = json.Unmarshal(body, &raw)
	}
	if err != nil || raw == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request body", nil)
		return
	}
	var req patchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request body", nil)
		return
	}
	_, nameSent := raw["name"]
	_, phoneSent := raw["phone"]
	fields := validateFields(req.Name, req.Phone, req.Email.Value, nameSent, phoneSent)
	if len(fields) > 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "validation_error", "invalid request", fields)
		return
	}

	c, err := h.store.Update(r.Context(), h.orgID, id, Patch{
		Name:     trimmed(req.Name),
		Phone:    req.Phone,
		SetEmail: req.Email.Set,
		Email:    emptyToNil(req.Email.Value),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.Delete(r.Context(), h.orgID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pathID parses the {id} segment. A malformed ID can never match a contact, so it is a 404.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "contact not found", nil)
		return uuid.UUID{}, false
	}
	return id, true
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", "contact not found", nil)
	case errors.Is(err, ErrPhoneTaken):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", "a contact with this phone already exists", nil)
	default:
		slog.Error("contact handler", "requestId", httpx.RequestID(r), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
	}
}
