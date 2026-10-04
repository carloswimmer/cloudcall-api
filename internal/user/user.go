package user

import (
	"errors"

	"github.com/google/uuid"
)

type Presence string

const (
	PresenceAvailable Presence = "available"
	PresenceBusy      Presence = "busy"
	PresenceOffline   Presence = "offline"
)

// Valid reports whether p is one of the known presence values.
func (p Presence) Valid() bool {
	switch p {
	case PresenceAvailable, PresenceBusy, PresenceOffline:
		return true
	}
	return false
}

type User struct {
	ID                 uuid.UUID `json:"id"`
	OrganizationID     uuid.UUID `json:"-"`
	Name               string    `json:"name"`
	Extension          string    `json:"extension"`
	Presence           Presence  `json:"presence"`
	PresenceBeforeBusy *Presence `json:"-"`
	Version            int       `json:"version"`
}

type Organization struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	BrandColor string    `json:"brandColor"`
	Timezone   string    `json:"timezone"`
}

var (
	// ErrNotFound is returned when the user or organization does not exist.
	ErrNotFound = errors.New("not found")
	// ErrVersionMismatch is returned when the expected version is stale.
	ErrVersionMismatch = errors.New("version mismatch")
)
