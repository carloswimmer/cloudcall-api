package contact

import (
	"log/slog"
	"net/http"

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
