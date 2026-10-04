package user

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"cloudcall/internal/event"

	"github.com/google/uuid"
)

// Store reads and updates users and organizations in PostgreSQL.
type Store struct {
	DB *sql.DB
	// Publisher receives a presence.updated envelope after every committed
	// presence change. Nil is treated as event.Nop.
	Publisher event.Publisher
}

func (s *Store) publisher() event.Publisher {
	if s.Publisher == nil {
		return event.Nop{}
	}
	return s.Publisher
}

// PublishPresence announces the full user as presence.updated. Call it only
// after the transaction that produced u has committed.
func (s *Store) PublishPresence(u User) {
	env, err := event.NewEnvelope(event.TypePresenceUpdated, u.ID, u.Version, u)
	if err != nil {
		slog.Error("build presence event", "error", err)
		return
	}
	s.publisher().Publish(env)
}

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

// GetTx reads one user inside tx, so a caller sees its own uncommitted changes.
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, orgID, userID uuid.UUID) (User, error) {
	u, err := scanUser(tx.QueryRowContext(ctx,
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
	if err != nil {
		return User{}, err
	}
	s.PublishPresence(u)
	return u, nil
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

// MarkBusy sets the user to busy inside tx, remembering the previous presence in
// presence_before_busy (only if not already remembered) and bumping the version.
func (s *Store) MarkBusy(ctx context.Context, tx *sql.Tx, orgID, userID uuid.UUID) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE users
		 SET presence_before_busy = COALESCE(presence_before_busy, presence),
		     presence = 'busy',
		     version = version + 1
		 WHERE id = $1 AND organization_id = $2`, userID, orgID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// LockUser takes a row lock on the user inside tx (SELECT ... FOR UPDATE).
// Call persistence always locks the user row before touching any call row, so
// Create, Apply and the startup sweep cannot deadlock on opposite lock orders.
func (s *Store) LockUser(ctx context.Context, tx *sql.Tx, orgID, userID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE id = $1 AND organization_id = $2 FOR UPDATE`, userID, orgID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// RestorePresence puts back the presence saved by MarkBusy inside tx, clears
// presence_before_busy and bumps the version. It is a no-op when nothing was
// saved; the bool is whether a row was updated.
func (s *Store) RestorePresence(ctx context.Context, tx *sql.Tx, orgID, userID uuid.UUID) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE users
		 SET presence = presence_before_busy,
		     presence_before_busy = NULL,
		     version = version + 1
		 WHERE id = $1 AND organization_id = $2 AND presence_before_busy IS NOT NULL`, userID, orgID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
