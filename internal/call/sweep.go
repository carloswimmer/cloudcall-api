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
