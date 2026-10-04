package call

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// SweepInterrupted fails every non-terminal call of the organization with the
// reason simulation_interrupted and restores each owner's presence. Calls left
// over from a previous process cannot continue, because their simulation timers
// are gone.
//
// This is deliberately written here and not through Next: Next rejects
// simulation_interrupted as a client-supplied reason.
func (s *Store) SweepInterrupted(ctx context.Context, orgID uuid.UUID) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Lock order is always user rows, then call rows (same as Create and Apply).
	// Owners are locked in a stable order so concurrent sweeps cannot deadlock either.
	ownerRows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT owner_user_id FROM calls
		 WHERE organization_id = $1 AND status IN ('dialing', 'ringing', 'active')
		 ORDER BY owner_user_id`, orgID)
	if err != nil {
		return fmt.Errorf("select interrupted owners: %w", err)
	}
	var owners []uuid.UUID
	for ownerRows.Next() {
		var id uuid.UUID
		if err := ownerRows.Scan(&id); err != nil {
			_ = ownerRows.Close()
			return err
		}
		owners = append(owners, id)
	}
	if err := ownerRows.Err(); err != nil {
		_ = ownerRows.Close()
		return err
	}
	_ = ownerRows.Close()
	for _, id := range owners {
		if err := s.Users.LockUser(ctx, tx, orgID, id); err != nil {
			return fmt.Errorf("lock owner: %w", err)
		}
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT `+callColumns+` FROM calls
		 WHERE organization_id = $1 AND status IN ('dialing', 'ringing', 'active')
		 ORDER BY created_at FOR UPDATE`, orgID)
	if err != nil {
		return fmt.Errorf("select interrupted calls: %w", err)
	}
	var leftovers []Call
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		leftovers = append(leftovers, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	now := s.now()
	reason := ReasonSimulationInterrupted
	for _, c := range leftovers {
		from := c.Status
		c.Status = StatusFailed
		c.Version++
		c.EndedAt = &now
		c.FailureReason = &reason

		if err := updateCall(ctx, tx, c); err != nil {
			return err
		}
		if err := insertTransition(ctx, tx, c.ID, Transition{
			From: &from, To: StatusFailed, OccurredAt: now, Reason: &reason,
		}); err != nil {
			return err
		}
		if err := s.Users.RestorePresence(ctx, tx, orgID, c.OwnerUserID); err != nil {
			return fmt.Errorf("restore presence: %w", err)
		}
	}
	return tx.Commit()
}
