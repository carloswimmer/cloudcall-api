// Package call holds the call domain model and its pure state machine.
package call

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

type Status string

const (
	StatusDialing  Status = "dialing"
	StatusRinging  Status = "ringing"
	StatusActive   Status = "active"
	StatusEnded    Status = "ended"
	StatusRejected Status = "rejected"
	StatusMissed   Status = "missed"
	StatusFailed   Status = "failed"
)

// IsTerminal reports whether s is a final status (no further transitions).
func IsTerminal(s Status) bool {
	switch s {
	case StatusEnded, StatusRejected, StatusMissed, StatusFailed:
		return true
	}
	return false
}

type Reason string

const (
	ReasonNoAnswer              Reason = "no_answer"
	ReasonRingTimeout           Reason = "ring_timeout"
	ReasonNetworkError          Reason = "network_error"
	ReasonSimulationInterrupted Reason = "simulation_interrupted"
)

type Action string

const (
	ActionAnswer  Action = "answer"
	ActionReject  Action = "reject"
	ActionEnd     Action = "end"
	ActionConnect Action = "connect"
	ActionFail    Action = "fail"
)

type Call struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	OwnerUserID       uuid.UUID
	ContactID         *uuid.UUID
	PeerNameSnapshot  string
	PeerPhoneSnapshot string
	Direction         Direction
	Status            Status
	Version           int
	CreatedAt         time.Time
	StartedAt         *time.Time
	EndedAt           *time.Time
	FailureReason     *Reason
}

// Transition records one status change (a row of the call timeline).
type Transition struct {
	From       *Status
	To         Status
	OccurredAt time.Time
	Reason     *Reason
}

type Command struct {
	Action          Action
	ExpectedVersion int
	Reason          *Reason
}

var (
	ErrInvalidTransition = errors.New("invalid_transition")
	ErrVersionMismatch   = errors.New("version_mismatch")
	ErrValidation        = errors.New("validation_error")
	// ErrActiveCallExists means the owner already has a non-terminal call.
	ErrActiveCallExists = errors.New("active_call_exists")
)

// Next applies cmd to c at time now. It is pure: c is never modified and
// no I/O happens. On error the returned Call is c unchanged and the
// Transition is the zero value.
func Next(c Call, cmd Command, now time.Time) (Call, Transition, error) {
	if cmd.ExpectedVersion != c.Version {
		return c, Transition{}, ErrVersionMismatch
	}
	if IsTerminal(c.Status) {
		return c, Transition{}, ErrInvalidTransition
	}

	var (
		to  Status
		err error
	)
	switch c.Direction {
	case DirectionOutbound:
		to, err = nextOutbound(c.Status, cmd)
	case DirectionInbound:
		to, err = nextInbound(c.Status, cmd)
	default:
		err = ErrValidation
	}
	if err != nil {
		return c, Transition{}, err
	}

	from := c.Status
	next := c
	next.Status = to
	next.Version = c.Version + 1
	if to == StatusActive {
		t := now
		next.StartedAt = &t
	}
	if IsTerminal(to) {
		t := now
		next.EndedAt = &t
	}

	var reason *Reason
	if cmd.Action == ActionFail {
		r := *cmd.Reason // non-nil: nextOutbound/nextInbound validated it
		reason = &r
		fr := r
		next.FailureReason = &fr
	}

	return next, Transition{From: &from, To: to, OccurredAt: now, Reason: reason}, nil
}

func nextOutbound(from Status, cmd Command) (Status, error) {
	switch cmd.Action {
	case ActionEnd:
		switch from {
		case StatusDialing, StatusRinging, StatusActive:
			return StatusEnded, nil
		}
	case ActionConnect:
		switch from {
		case StatusDialing:
			return StatusRinging, nil
		case StatusRinging:
			return StatusActive, nil
		}
	case ActionFail:
		if cmd.Reason == nil {
			return "", ErrValidation
		}
		switch *cmd.Reason {
		case ReasonNoAnswer:
			switch from {
			case StatusDialing, StatusRinging:
				return StatusFailed, nil
			}
		case ReasonNetworkError:
			return StatusFailed, nil
		default: // ring_timeout, simulation_interrupted, unknown
			return "", ErrValidation
		}
	case ActionAnswer, ActionReject:
		// Only meaningful for inbound calls.
	default:
		return "", ErrValidation
	}
	return "", ErrInvalidTransition
}

func nextInbound(from Status, cmd Command) (Status, error) {
	switch cmd.Action {
	case ActionAnswer:
		if from == StatusRinging {
			return StatusActive, nil
		}
	case ActionReject:
		if from == StatusRinging {
			return StatusRejected, nil
		}
	case ActionEnd:
		if from == StatusActive {
			return StatusEnded, nil
		}
	case ActionFail:
		if cmd.Reason == nil {
			return "", ErrValidation
		}
		switch *cmd.Reason {
		case ReasonRingTimeout:
			if from == StatusRinging {
				return StatusMissed, nil
			}
		case ReasonNetworkError:
			return StatusFailed, nil
		default: // no_answer, simulation_interrupted, unknown
			return "", ErrValidation
		}
	case ActionConnect:
		// Only meaningful for outbound calls.
	default:
		return "", ErrValidation
	}
	return "", ErrInvalidTransition
}
