// Package event is the in-memory SSE hub. It knows nothing about calls or users:
// the stores publish ready-made envelopes and the application injects a loader.
package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Envelope types sent over the stream.
const (
	TypeSnapshot        = "snapshot"
	TypeCallUpdated     = "call.updated"
	TypePresenceUpdated = "presence.updated"
	TypeHeartbeat       = "heartbeat"
)

// Envelope is the single wire shape of every SSE event.
type Envelope struct {
	EventID       string          `json:"eventId"`
	Type          string          `json:"type"`
	OccurredAt    time.Time       `json:"occurredAt"`
	EntityID      *string         `json:"entityId,omitempty"`
	EntityVersion *int            `json:"entityVersion,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// Publisher is what the stores use to announce a committed change.
type Publisher interface {
	Publish(Envelope)
}

// Nop is a Publisher that drops everything.
type Nop struct{}

func (Nop) Publish(Envelope) {}

// Snapshot is the state sent first on every connection. Calls holds the
// non-terminal calls, Users every user, Tombstones the recently ended calls.
type Snapshot struct {
	Calls      []any
	Users      []any
	Tombstones []Tombstone
}

// Tombstone remembers that a call reached version Version (usually a terminal
// status), so an older call.updated that arrives later cannot bring it back.
type Tombstone struct {
	ID      uuid.UUID `json:"id"`
	Version int       `json:"version"`
	Status  string    `json:"status"`
}

// SnapshotLoader reads the current state from the source of truth.
type SnapshotLoader interface {
	LoadSnapshot(ctx context.Context) (Snapshot, error)
}

// NewEnvelope builds a versioned entity event (call.updated, presence.updated)
// with the full entity as payload and a process-unique event ID.
func NewEnvelope(eventType string, entityID uuid.UUID, entityVersion int, entity any) (Envelope, error) {
	payload, err := json.Marshal(entity)
	if err != nil {
		return Envelope{}, err
	}
	id := entityID.String()
	return Envelope{
		EventID:       uuid.NewString(),
		Type:          eventType,
		OccurredAt:    time.Now().UTC(),
		EntityID:      &id,
		EntityVersion: &entityVersion,
		Payload:       payload,
	}, nil
}

// Prefer is the rule a client applies to every incoming event: given the version
// it already holds for the entity (from the snapshot, a tombstone or an earlier
// event), keep the incoming envelope only if it is not older. Versions are
// monotonic per entity, not a global order, and the hub does not filter, so this
// check is what stops an older call.updated from resurrecting an ended call.
// Envelopes without a version (heartbeat, snapshot) are always kept.
func Prefer(existingVersion int, incoming Envelope) bool {
	if incoming.EntityVersion == nil {
		return true
	}
	return *incoming.EntityVersion >= existingVersion
}
