package contact

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Store reads contacts from PostgreSQL. Every query is scoped to an organization.
type Store struct {
	DB *sql.DB
}

const (
	whereClause = `organization_id = $1 AND ($2 = '' OR name ILIKE '%' || $2 || '%' OR phone ILIKE '%' || $2 || '%')`

	countSQL = `SELECT COUNT(*) FROM contacts WHERE ` + whereClause

	listSQL = `SELECT id, organization_id, name, phone, email, created_at, updated_at
		FROM contacts WHERE ` + whereClause + `
		ORDER BY name ASC, id ASC LIMIT $3 OFFSET $4`
)

// List returns one page of contacts matching q (name or phone, case-insensitive),
// ordered by name then id, plus the total number of matches.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, q string, page, size int) (Page[Contact], error) {
	out := Page[Contact]{Items: []Contact{}, Page: page, PageSize: size}

	if err := s.DB.QueryRowContext(ctx, countSQL, orgID, q).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count contacts: %w", err)
	}

	offset, err := listOffset(page, size)
	if err != nil {
		return out, fmt.Errorf("list contacts: %w", err)
	}
	rows, err := s.DB.QueryContext(ctx, listSQL, orgID, q, size, offset)
	if err != nil {
		return out, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Contact
		var email sql.NullString
		if err := rows.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Phone, &email, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return out, fmt.Errorf("scan contact: %w", err)
		}
		if email.Valid {
			c.Email = &email.String
		}
		out.Items = append(out.Items, c)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("list contacts: %w", err)
	}
	return out, nil
}
