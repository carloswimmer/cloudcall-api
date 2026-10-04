package user

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// Store reads and updates users and organizations in PostgreSQL.
type Store struct{ DB *sql.DB }

const userColumns = `id, organization_id, name, extension, presence, presence_before_busy, version`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(row rowScanner) (User, error) {
	var (
		u      User
		before sql.NullString
	)
	if err := row.Scan(&u.ID, &u.OrganizationID, &u.Name, &u.Extension, &u.Presence, &before, &u.Version); err != nil {
		return User{}, err
	}
	if before.Valid {
		p := Presence(before.String)
		u.PresenceBeforeBusy = &p
	}
	return u, nil
}

// Get returns one user scoped to the organization.
func (s *Store) Get(ctx context.Context, orgID, userID uuid.UUID) (User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1 AND organization_id = $2`, userID, orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// List returns every user of the organization ordered by extension.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE organization_id = $1 ORDER BY extension`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetPresence updates presence with optimistic concurrency. A stale
// expectedVersion (or an unknown user) yields ErrVersionMismatch.
func (s *Store) SetPresence(ctx context.Context, orgID, userID uuid.UUID, presence Presence, expectedVersion int) (User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx,
		`UPDATE users SET presence = $1, version = version + 1
		 WHERE id = $2 AND organization_id = $3 AND version = $4
		 RETURNING `+userColumns,
		string(presence), userID, orgID, expectedVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrVersionMismatch
	}
	return u, err
}

// Organization returns the organization by ID.
func (s *Store) Organization(ctx context.Context, orgID uuid.UUID) (Organization, error) {
	var o Organization
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, brand_color, timezone FROM organizations WHERE id = $1`, orgID).
		Scan(&o.ID, &o.Name, &o.BrandColor, &o.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	return o, err
}
