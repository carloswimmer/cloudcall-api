package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrNotFound means no contact with that ID exists in the organization.
	ErrNotFound = errors.New("contact not found")
	// ErrPhoneTaken means another contact in the organization already has the phone.
	ErrPhoneTaken = errors.New("contact phone already exists")
)

// pgUniqueViolation is the PostgreSQL SQLSTATE for unique_violation.
const pgUniqueViolation = "23505"

// Store reads and writes contacts in PostgreSQL. Every query is scoped to an organization.
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
		c, err := scanContact(rows)
		if err != nil {
			return out, fmt.Errorf("scan contact: %w", err)
		}
		out.Items = append(out.Items, c)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("list contacts: %w", err)
	}
	return out, nil
}

const (
	contactColumns = `id, organization_id, name, phone, email, created_at, updated_at`

	insertSQL = `INSERT INTO contacts (id, organization_id, name, phone, email, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6) RETURNING ` + contactColumns

	// $3/$5/$7 say whether name/phone/email were sent; unsent columns keep their value.
	updateSQL = `UPDATE contacts SET
			name = CASE WHEN $3 THEN $4 ELSE name END,
			phone = CASE WHEN $5 THEN $6 ELSE phone END,
			email = CASE WHEN $7 THEN $8 ELSE email END,
			updated_at = $9
		WHERE id = $1 AND organization_id = $2 RETURNING ` + contactColumns

	deleteSQL = `DELETE FROM contacts WHERE id = $1 AND organization_id = $2`
)

type rowScanner interface{ Scan(dest ...any) error }

func scanContact(row rowScanner) (Contact, error) {
	var c Contact
	var email sql.NullString
	if err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Phone, &email, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Contact{}, err
	}
	if email.Valid {
		c.Email = &email.String
	}
	return c, nil
}

// mapWriteError turns a unique violation into ErrPhoneTaken.
func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return ErrPhoneTaken
	}
	return err
}

// NewContact is the validated input for Create. A nil Email stores no email.
type NewContact struct {
	Name  string
	Phone string
	Email *string
}

// Create inserts a contact into the organization. A duplicate phone yields ErrPhoneTaken.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, in NewContact) (Contact, error) {
	c, err := scanContact(s.DB.QueryRowContext(ctx, insertSQL,
		uuid.New(), orgID, in.Name, in.Phone, nullString(in.Email), time.Now().UTC()))
	if err != nil {
		return Contact{}, fmt.Errorf("create contact: %w", mapWriteError(err))
	}
	return c, nil
}

// Patch holds the fields to change. A nil Name or Phone is left untouched;
// SetEmail with a nil Email clears the email.
type Patch struct {
	Name     *string
	Phone    *string
	SetEmail bool
	Email    *string
}

// Update applies a partial change. An unknown ID yields ErrNotFound and a
// duplicate phone yields ErrPhoneTaken.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, p Patch) (Contact, error) {
	c, err := scanContact(s.DB.QueryRowContext(ctx, updateSQL,
		id, orgID,
		p.Name != nil, nullString(p.Name),
		p.Phone != nil, nullString(p.Phone),
		p.SetEmail, nullString(p.Email),
		time.Now().UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	if err != nil {
		return Contact{}, fmt.Errorf("update contact: %w", mapWriteError(err))
	}
	return c, nil
}

// Delete removes a contact. An unknown ID yields ErrNotFound.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	res, err := s.DB.ExecContext(ctx, deleteSQL, id, orgID)
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
