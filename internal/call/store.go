package call

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"cloudcall/internal/user"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound means no call with that ID exists in the organization.
var ErrNotFound = errors.New("call not found")

// pgUniqueViolation is the PostgreSQL SQLSTATE for unique_violation.
const pgUniqueViolation = "23505"

// Note is the free-text note attached to a call (table call_notes).
type Note struct {
	CallID    uuid.UUID
	Text      string
	UpdatedAt time.Time
}

// Store persists calls and their transitions. Every query is scoped to an organization.
type Store struct {
	DB    *sql.DB
	Users *user.Store
	Now   func() time.Time
}

// now returns the store clock at PostgreSQL's microsecond precision, in UTC.
func (s *Store) now() time.Time {
	t := time.Now()
	if s.Now != nil {
		t = s.Now()
	}
	return t.UTC().Truncate(time.Microsecond)
}

const callColumns = `id, organization_id, owner_user_id, contact_id, peer_name_snapshot, peer_phone_snapshot,
	direction, status, version, created_at, started_at, ended_at, failure_reason`

type rowScanner interface{ Scan(dest ...any) error }

func scanCall(row rowScanner) (Call, error) {
	var (
		c       Call
		contact uuid.NullUUID
		started sql.NullTime
		ended   sql.NullTime
		reason  sql.NullString
	)
	if err := row.Scan(&c.ID, &c.OrganizationID, &c.OwnerUserID, &contact, &c.PeerNameSnapshot,
		&c.PeerPhoneSnapshot, &c.Direction, &c.Status, &c.Version, &c.CreatedAt,
		&started, &ended, &reason); err != nil {
		return Call{}, err
	}
	if contact.Valid {
		id := contact.UUID
		c.ContactID = &id
	}
	c.CreatedAt = c.CreatedAt.UTC()
	if started.Valid {
		t := started.Time.UTC()
		c.StartedAt = &t
	}
	if ended.Valid {
		t := ended.Time.UTC()
		c.EndedAt = &t
	}
	if reason.Valid {
		r := Reason(reason.String)
		c.FailureReason = &r
	}
	return c, nil
}

// Create starts a call for the owner (dialing for outbound, ringing for inbound),
// records the first transition and marks the owner busy, all in one transaction.
// A second non-terminal call for the same owner yields ErrActiveCallExists.
func (s *Store) Create(ctx context.Context, orgID, ownerID uuid.UUID, direction Direction, contactID *uuid.UUID, peerName, peerPhone string) (Call, error) {
	var status Status
	switch direction {
	case DirectionOutbound:
		status = StatusDialing
	case DirectionInbound:
		status = StatusRinging
	default:
		return Call{}, ErrValidation
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Call{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// MarkBusy locks the owner row first, which serializes concurrent Creates.
	if err := s.Users.MarkBusy(ctx, tx, orgID, ownerID); err != nil {
		return Call{}, err
	}

	now := s.now()
	c := Call{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		OwnerUserID:       ownerID,
		ContactID:         contactID,
		PeerNameSnapshot:  peerName,
		PeerPhoneSnapshot: peerPhone,
		Direction:         direction,
		Status:            status,
		Version:           1,
		CreatedAt:         now,
	}
	var contact uuid.NullUUID
	if contactID != nil {
		contact = uuid.NullUUID{UUID: *contactID, Valid: true}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO calls (id, organization_id, owner_user_id, contact_id, peer_name_snapshot,
			peer_phone_snapshot, direction, status, version, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, $9)`,
		c.ID, orgID, ownerID, contact, peerName, peerPhone, string(direction), string(status), now); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return Call{}, ErrActiveCallExists
		}
		return Call{}, fmt.Errorf("insert call: %w", err)
	}
	if err := insertTransition(ctx, tx, c.ID, Transition{To: status, OccurredAt: now}); err != nil {
		return Call{}, err
	}
	if err := tx.Commit(); err != nil {
		return Call{}, err
	}
	return c, nil
}

// Apply loads the call under a row lock, runs Next, persists the call and its
// transition, and restores the owner's presence when the new status is terminal.
func (s *Store) Apply(ctx context.Context, orgID uuid.UUID, callID uuid.UUID, cmd Command) (Call, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Call{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Lock order is always user row, then call row (same as Create). The owner
	// is read without a lock, the user row is locked, and only then is the call
	// row locked and reloaded. The owner of a call never changes.
	var ownerID uuid.UUID
	err = tx.QueryRowContext(ctx,
		`SELECT owner_user_id FROM calls WHERE id = $1 AND organization_id = $2`, callID, orgID).Scan(&ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Call{}, ErrNotFound
	}
	if err != nil {
		return Call{}, err
	}
	if err := s.Users.LockUser(ctx, tx, orgID, ownerID); err != nil {
		return Call{}, fmt.Errorf("lock owner: %w", err)
	}

	c, err := scanCall(tx.QueryRowContext(ctx,
		`SELECT `+callColumns+` FROM calls WHERE id = $1 AND organization_id = $2 FOR UPDATE`, callID, orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return Call{}, ErrNotFound
	}
	if err != nil {
		return Call{}, err
	}

	next, tr, err := Next(c, cmd, s.now())
	if err != nil {
		return Call{}, err
	}
	if err := updateCall(ctx, tx, next); err != nil {
		return Call{}, err
	}
	if err := insertTransition(ctx, tx, next.ID, tr); err != nil {
		return Call{}, err
	}
	if IsTerminal(next.Status) {
		if err := s.Users.RestorePresence(ctx, tx, orgID, next.OwnerUserID); err != nil {
			return Call{}, fmt.Errorf("restore presence: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Call{}, err
	}
	return next, nil
}

// Get returns the call, its transitions in chronological order and its note (nil if none).
func (s *Store) Get(ctx context.Context, orgID, callID uuid.UUID) (Call, []Transition, *Note, error) {
	c, err := scanCall(s.DB.QueryRowContext(ctx,
		`SELECT `+callColumns+` FROM calls WHERE id = $1 AND organization_id = $2`, callID, orgID))
	if errors.Is(err, sql.ErrNoRows) {
		return Call{}, nil, nil, ErrNotFound
	}
	if err != nil {
		return Call{}, nil, nil, err
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT from_status, to_status, occurred_at, reason FROM call_transitions
		 WHERE call_id = $1 ORDER BY occurred_at, from_status NULLS FIRST`, callID)
	if err != nil {
		return Call{}, nil, nil, err
	}
	defer rows.Close()
	transitions := []Transition{}
	for rows.Next() {
		var (
			t      Transition
			from   sql.NullString
			reason sql.NullString
		)
		if err := rows.Scan(&from, &t.To, &t.OccurredAt, &reason); err != nil {
			return Call{}, nil, nil, err
		}
		t.OccurredAt = t.OccurredAt.UTC()
		if from.Valid {
			f := Status(from.String)
			t.From = &f
		}
		if reason.Valid {
			r := Reason(reason.String)
			t.Reason = &r
		}
		transitions = append(transitions, t)
	}
	if err := rows.Err(); err != nil {
		return Call{}, nil, nil, err
	}

	var note *Note
	n := Note{CallID: callID}
	err = s.DB.QueryRowContext(ctx,
		`SELECT text, updated_at FROM call_notes WHERE call_id = $1`, callID).Scan(&n.Text, &n.UpdatedAt)
	switch {
	case err == nil:
		n.UpdatedAt = n.UpdatedAt.UTC()
		note = &n
	case !errors.Is(err, sql.ErrNoRows):
		return Call{}, nil, nil, err
	}
	return c, transitions, note, nil
}

// ListFilter narrows List. Every non-nil field is ANDed; nil means "any".
// From and To bound created_at and are both inclusive.
type ListFilter struct {
	Direction *Direction
	Status    *Status
	Terminal  *bool
	From      *time.Time
	To        *time.Time
}

// listWhere is shared by the count and the page query. A combination that can
// never match (for example status=ended with terminal=false) simply yields no rows.
const listWhere = `organization_id = $1
	AND ($2::text IS NULL OR direction = $2)
	AND ($3::text IS NULL OR status = $3)
	AND ($4::boolean IS NULL OR (status IN ('ended', 'rejected', 'missed', 'failed')) = $4)
	AND ($5::timestamptz IS NULL OR created_at >= $5)
	AND ($6::timestamptz IS NULL OR created_at <= $6)`

// List returns one page of the organization's calls, newest first (created_at
// DESC, id DESC), plus the total number of calls matching the filter. The caller
// validates limit and offset (see contact.ParsePage).
func (s *Store) List(ctx context.Context, orgID uuid.UUID, f ListFilter, limit, offset int) ([]Call, int, error) {
	var (
		direction sql.NullString
		status    sql.NullString
		terminal  sql.NullBool
		from      sql.NullTime
		to        sql.NullTime
	)
	if f.Direction != nil {
		direction = sql.NullString{String: string(*f.Direction), Valid: true}
	}
	if f.Status != nil {
		status = sql.NullString{String: string(*f.Status), Valid: true}
	}
	if f.Terminal != nil {
		terminal = sql.NullBool{Bool: *f.Terminal, Valid: true}
	}
	if f.From != nil {
		from = sql.NullTime{Time: *f.From, Valid: true}
	}
	if f.To != nil {
		to = sql.NullTime{Time: *f.To, Valid: true}
	}

	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM calls WHERE `+listWhere,
		orgID, direction, status, terminal, from, to).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count calls: %w", err)
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+callColumns+` FROM calls WHERE `+listWhere+`
		 ORDER BY created_at DESC, id DESC LIMIT $7 OFFSET $8`,
		orgID, direction, status, terminal, from, to, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list calls: %w", err)
	}
	defer rows.Close()
	calls := []Call{}
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan call: %w", err)
		}
		calls = append(calls, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list calls: %w", err)
	}
	return calls, total, nil
}

// SetNote creates or replaces the note of a call. Only a terminal call may have
// a note: a non-terminal one yields ErrInvalidTransition and an unknown one
// ErrNotFound. The status check and the upsert are one statement, and a terminal
// status never changes back, so there is no window between them.
func (s *Store) SetNote(ctx context.Context, orgID, callID uuid.UUID, text string) (Note, error) {
	n := Note{CallID: callID}
	err := s.DB.QueryRowContext(ctx,
		`INSERT INTO call_notes (call_id, text, updated_at)
		 SELECT id, $3, $4 FROM calls
		 WHERE id = $1 AND organization_id = $2 AND status IN ('ended', 'rejected', 'missed', 'failed')
		 ON CONFLICT (call_id) DO UPDATE SET text = EXCLUDED.text, updated_at = EXCLUDED.updated_at
		 RETURNING text, updated_at`,
		callID, orgID, text, s.now()).Scan(&n.Text, &n.UpdatedAt)
	if err == nil {
		n.UpdatedAt = n.UpdatedAt.UTC()
		return n, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Note{}, fmt.Errorf("upsert note: %w", err)
	}

	// No row was written: either the call does not exist here or it is not terminal.
	var exists bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM calls WHERE id = $1 AND organization_id = $2)`,
		callID, orgID).Scan(&exists); err != nil {
		return Note{}, err
	}
	if !exists {
		return Note{}, ErrNotFound
	}
	return Note{}, ErrInvalidTransition
}

// HasNonTerminal reports whether the user has a dialing, ringing or active call.
func (s *Store) HasNonTerminal(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var has bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM calls
			WHERE organization_id = $1 AND owner_user_id = $2
			  AND status IN ('dialing', 'ringing', 'active'))`, orgID, userID).Scan(&has)
	return has, err
}

func updateCall(ctx context.Context, tx *sql.Tx, c Call) error {
	var reason sql.NullString
	if c.FailureReason != nil {
		reason = sql.NullString{String: string(*c.FailureReason), Valid: true}
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE calls SET status = $2, version = $3, started_at = $4, ended_at = $5, failure_reason = $6
		 WHERE id = $1`,
		c.ID, string(c.Status), c.Version, c.StartedAt, c.EndedAt, reason)
	if err != nil {
		return fmt.Errorf("update call: %w", err)
	}
	return nil
}

func insertTransition(ctx context.Context, tx *sql.Tx, callID uuid.UUID, t Transition) error {
	var (
		from   sql.NullString
		reason sql.NullString
	)
	if t.From != nil {
		from = sql.NullString{String: string(*t.From), Valid: true}
	}
	if t.Reason != nil {
		reason = sql.NullString{String: string(*t.Reason), Valid: true}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO call_transitions (id, call_id, from_status, to_status, occurred_at, reason)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.New(), callID, from, string(t.To), t.OccurredAt, reason)
	if err != nil {
		return fmt.Errorf("insert transition: %w", err)
	}
	return nil
}
