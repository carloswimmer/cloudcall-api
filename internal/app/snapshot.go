package app

import (
	"context"
	"fmt"
	"time"

	"cloudcall/internal/call"
	"cloudcall/internal/event"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

const (
	// tombstoneWindow is how long an ended call is remembered by the snapshot.
	tombstoneWindow = 15 * time.Minute
	// tombstoneLimit caps the tombstones in one snapshot.
	tombstoneLimit = 100
)

// snapshotLoader reads the stream's first event from PostgreSQL: the active
// calls, every user and the demo owner's recently ended calls. It lives here
// (not in event) so the event package never imports call or user.
type snapshotLoader struct {
	calls   *call.Store
	users   *user.Store
	orgID   uuid.UUID
	ownerID uuid.UUID
	now     func() time.Time
}

func (l snapshotLoader) LoadSnapshot(ctx context.Context) (event.Snapshot, error) {
	active, err := l.calls.Active(ctx, l.orgID)
	if err != nil {
		return event.Snapshot{}, fmt.Errorf("load active calls: %w", err)
	}
	users, err := l.users.List(ctx, l.orgID)
	if err != nil {
		return event.Snapshot{}, fmt.Errorf("load users: %w", err)
	}
	ended, err := l.calls.RecentTerminal(ctx, l.orgID, l.ownerID, l.now().Add(-tombstoneWindow), tombstoneLimit)
	if err != nil {
		return event.Snapshot{}, fmt.Errorf("load tombstones: %w", err)
	}

	snap := event.Snapshot{
		Calls:      make([]any, 0, len(active)),
		Users:      make([]any, 0, len(users)),
		Tombstones: make([]event.Tombstone, 0, len(ended)),
	}
	for _, c := range active {
		snap.Calls = append(snap.Calls, call.Payload(c))
	}
	for _, u := range users {
		snap.Users = append(snap.Users, u)
	}
	for _, c := range ended {
		snap.Tombstones = append(snap.Tombstones, event.Tombstone{ID: c.ID, Version: c.Version, Status: string(c.Status)})
	}
	return snap, nil
}
