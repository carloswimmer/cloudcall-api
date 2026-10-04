// Package dashboard serves the day aggregates shown on the CloudCall Desk home screen.
package dashboard

import (
	"database/sql"
	"log/slog"
	"net/http"

	"cloudcall/internal/platform/httpx"

	"github.com/google/uuid"
)

// Handler serves GET /api/v1/dashboard for the seeded organization and demo owner.
// The client never supplies organization or user IDs.
type Handler struct {
	db    *sql.DB
	orgID uuid.UUID
	owner uuid.UUID
}

func NewHandler(db *sql.DB, orgID, ownerID uuid.UUID) *Handler {
	return &Handler{db: db, orgID: orgID, owner: ownerID}
}

// Register mounts the dashboard route on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/dashboard", h.get)
}

type response struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
	Total    int    `json:"total"`
	Inbound  int    `json:"inbound"`
	Outbound int    `json:"outbound"`
	Missed   int    `json:"missed"`
	Active   int    `json:"active"`
}

// aggregateSQL computes everything in one round trip. The organization's own
// timezone ($1 is the organization id) defines "today" on both sides of the
// comparison, so the database clock and the stored instants agree on the day.
// "active" ignores the day: it counts the owner's (=$2) non-terminal calls.
const aggregateSQL = `
SELECT o.timezone,
       to_char(o.today, 'YYYY-MM-DD'),
       count(c.id) FILTER (WHERE c.is_today),
       count(c.id) FILTER (WHERE c.is_today AND c.direction = 'inbound'),
       count(c.id) FILTER (WHERE c.is_today AND c.direction = 'outbound'),
       count(c.id) FILTER (WHERE c.is_today AND c.status = 'missed'),
       count(c.id) FILTER (WHERE c.owner_user_id = $2 AND c.status IN ('dialing', 'ringing', 'active'))
FROM (
    SELECT id, timezone, (now() AT TIME ZONE timezone)::date AS today
    FROM organizations
    WHERE id = $1
) o
LEFT JOIN LATERAL (
    SELECT id, direction, status, owner_user_id,
           (created_at AT TIME ZONE o.timezone)::date = (now() AT TIME ZONE o.timezone)::date AS is_today
    FROM calls
    WHERE organization_id = o.id
) c ON true
GROUP BY o.id, o.timezone, o.today`

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	var out response
	err := h.db.QueryRowContext(r.Context(), aggregateSQL, h.orgID, h.owner).Scan(
		&out.Timezone, &out.Date, &out.Total, &out.Inbound, &out.Outbound, &out.Missed, &out.Active)
	if err != nil {
		slog.Error("dashboard handler", "requestId", httpx.RequestID(r), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
