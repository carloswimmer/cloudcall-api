package call

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	t0      = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	started = t0.Add(5 * time.Second)
	now     = t0.Add(30 * time.Second)
)

func reasonPtr(r Reason) *Reason { return &r }

func newCall(dir Direction, st Status) Call {
	c := Call{
		ID:               uuid.New(),
		OrganizationID:   uuid.New(),
		OwnerUserID:      uuid.New(),
		PeerNameSnapshot: "Ada Lovelace",
		Direction:        dir,
		Status:           st,
		Version:          3,
		CreatedAt:        t0,
	}
	if st == StatusActive {
		s := started
		c.StartedAt = &s
	}
	return c
}

func TestIsTerminal(t *testing.T) {
	cases := map[Status]bool{
		StatusDialing:  false,
		StatusRinging:  false,
		StatusActive:   false,
		StatusEnded:    true,
		StatusRejected: true,
		StatusMissed:   true,
		StatusFailed:   true,
	}
	for s, want := range cases {
		if got := IsTerminal(s); got != want {
			t.Errorf("IsTerminal(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestNext(t *testing.T) {
	noAnswer := reasonPtr(ReasonNoAnswer)
	ringTimeout := reasonPtr(ReasonRingTimeout)
	netErr := reasonPtr(ReasonNetworkError)
	interrupted := reasonPtr(ReasonSimulationInterrupted)

	type row struct {
		name   string
		dir    Direction
		from   Status
		action Action
		reason *Reason

		wantErr    error
		wantStatus Status
	}

	ok := func(name string, dir Direction, from Status, a Action, r *Reason, to Status) row {
		return row{name, dir, from, a, r, nil, to}
	}
	bad := func(name string, dir Direction, from Status, a Action, r *Reason, err error) row {
		return row{name, dir, from, a, r, err, ""}
	}
	in, out := DirectionInbound, DirectionOutbound
	inv, val := ErrInvalidTransition, ErrValidation

	rows := []row{
		// ---- Outbound: dialing ----
		bad("out dialing answer", out, StatusDialing, ActionAnswer, nil, inv),
		bad("out dialing reject", out, StatusDialing, ActionReject, nil, inv),
		ok("out dialing end", out, StatusDialing, ActionEnd, nil, StatusEnded),
		ok("out dialing connect", out, StatusDialing, ActionConnect, nil, StatusRinging),
		ok("out dialing fail no_answer", out, StatusDialing, ActionFail, noAnswer, StatusFailed),
		ok("out dialing fail network_error", out, StatusDialing, ActionFail, netErr, StatusFailed),
		bad("out dialing fail ring_timeout", out, StatusDialing, ActionFail, ringTimeout, val),
		bad("out dialing fail simulation_interrupted", out, StatusDialing, ActionFail, interrupted, val),

		// ---- Outbound: ringing ----
		bad("out ringing answer", out, StatusRinging, ActionAnswer, nil, inv),
		bad("out ringing reject", out, StatusRinging, ActionReject, nil, inv),
		ok("out ringing end", out, StatusRinging, ActionEnd, nil, StatusEnded),
		ok("out ringing connect", out, StatusRinging, ActionConnect, nil, StatusActive),
		ok("out ringing fail no_answer", out, StatusRinging, ActionFail, noAnswer, StatusFailed),
		ok("out ringing fail network_error", out, StatusRinging, ActionFail, netErr, StatusFailed),
		bad("out ringing fail ring_timeout", out, StatusRinging, ActionFail, ringTimeout, val),
		bad("out ringing fail simulation_interrupted", out, StatusRinging, ActionFail, interrupted, val),

		// ---- Outbound: active ----
		bad("out active answer", out, StatusActive, ActionAnswer, nil, inv),
		bad("out active reject", out, StatusActive, ActionReject, nil, inv),
		ok("out active end", out, StatusActive, ActionEnd, nil, StatusEnded),
		bad("out active connect", out, StatusActive, ActionConnect, nil, inv),
		bad("out active fail no_answer", out, StatusActive, ActionFail, noAnswer, inv),
		ok("out active fail network_error", out, StatusActive, ActionFail, netErr, StatusFailed),
		bad("out active fail ring_timeout", out, StatusActive, ActionFail, ringTimeout, val),
		bad("out active fail simulation_interrupted", out, StatusActive, ActionFail, interrupted, val),

		// ---- Inbound: ringing ----
		ok("in ringing answer", in, StatusRinging, ActionAnswer, nil, StatusActive),
		ok("in ringing reject", in, StatusRinging, ActionReject, nil, StatusRejected),
		bad("in ringing end", in, StatusRinging, ActionEnd, nil, inv),
		bad("in ringing connect", in, StatusRinging, ActionConnect, nil, inv),
		bad("in ringing fail no_answer", in, StatusRinging, ActionFail, noAnswer, val),
		ok("in ringing fail ring_timeout", in, StatusRinging, ActionFail, ringTimeout, StatusMissed),
		ok("in ringing fail network_error", in, StatusRinging, ActionFail, netErr, StatusFailed),
		bad("in ringing fail simulation_interrupted", in, StatusRinging, ActionFail, interrupted, val),

		// ---- Inbound: active ----
		bad("in active answer", in, StatusActive, ActionAnswer, nil, inv),
		bad("in active reject", in, StatusActive, ActionReject, nil, inv),
		ok("in active end", in, StatusActive, ActionEnd, nil, StatusEnded),
		bad("in active connect", in, StatusActive, ActionConnect, nil, inv),
		bad("in active fail no_answer", in, StatusActive, ActionFail, noAnswer, val),
		bad("in active fail ring_timeout", in, StatusActive, ActionFail, ringTimeout, inv),
		ok("in active fail network_error", in, StatusActive, ActionFail, netErr, StatusFailed),
		bad("in active fail simulation_interrupted", in, StatusActive, ActionFail, interrupted, val),

		// ---- Fail without a reason / unknown action / unknown direction ----
		bad("out dialing fail nil reason", out, StatusDialing, ActionFail, nil, val),
		bad("in ringing fail nil reason", in, StatusRinging, ActionFail, nil, val),
		bad("out dialing unknown action", out, StatusDialing, Action("hangup"), nil, val),
		bad("in ringing unknown action", in, StatusRinging, Action("hangup"), nil, val),
		bad("unknown direction", Direction("sideways"), StatusRinging, ActionEnd, nil, val),
	}

	// ---- Terminal states reject every action, in both directions ----
	for _, dir := range []Direction{out, in} {
		for _, st := range []Status{StatusEnded, StatusRejected, StatusMissed, StatusFailed} {
			for _, a := range []Action{ActionAnswer, ActionReject, ActionEnd, ActionConnect, ActionFail} {
				rows = append(rows, bad(
					string(dir)+" "+string(st)+" "+string(a),
					dir, st, a, netErr, inv,
				))
			}
		}
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			c := newCall(r.dir, r.from)
			orig := c
			cmd := Command{Action: r.action, ExpectedVersion: c.Version, Reason: r.reason}

			got, tr, err := Next(c, cmd, now)

			if r.wantErr != nil {
				if !errors.Is(err, r.wantErr) {
					t.Fatalf("err = %v, want %v", err, r.wantErr)
				}
				if got.Status != orig.Status || got.Version != orig.Version ||
					got.StartedAt != orig.StartedAt || got.EndedAt != nil || got.FailureReason != nil {
					t.Errorf("call changed on error: %+v", got)
				}
				if tr != (Transition{}) {
					t.Errorf("transition = %+v, want zero value", tr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.Status != r.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, r.wantStatus)
			}
			if got.Version != orig.Version+1 {
				t.Errorf("version = %d, want %d", got.Version, orig.Version+1)
			}

			// startedAt: set on entering active, otherwise preserved.
			switch {
			case r.wantStatus == StatusActive:
				if got.StartedAt == nil || !got.StartedAt.Equal(now) {
					t.Errorf("startedAt = %v, want %v", got.StartedAt, now)
				}
			case orig.StartedAt != nil:
				if got.StartedAt == nil || !got.StartedAt.Equal(*orig.StartedAt) {
					t.Errorf("startedAt = %v, want preserved %v", got.StartedAt, *orig.StartedAt)
				}
			default:
				if got.StartedAt != nil {
					t.Errorf("startedAt = %v, want nil", got.StartedAt)
				}
			}

			// endedAt: set only on entering a terminal status.
			if IsTerminal(r.wantStatus) {
				if got.EndedAt == nil || !got.EndedAt.Equal(now) {
					t.Errorf("endedAt = %v, want %v", got.EndedAt, now)
				}
			} else if got.EndedAt != nil {
				t.Errorf("endedAt = %v, want nil", got.EndedAt)
			}

			// failure reason only for fail action.
			if r.action == ActionFail {
				if got.FailureReason == nil || *got.FailureReason != *r.reason {
					t.Errorf("failureReason = %v, want %v", got.FailureReason, *r.reason)
				}
			} else if got.FailureReason != nil {
				t.Errorf("failureReason = %v, want nil", got.FailureReason)
			}

			// transition record
			if tr.From == nil || *tr.From != r.from {
				t.Errorf("transition.From = %v, want %q", tr.From, r.from)
			}
			if tr.To != r.wantStatus {
				t.Errorf("transition.To = %q, want %q", tr.To, r.wantStatus)
			}
			if !tr.OccurredAt.Equal(now) {
				t.Errorf("transition.OccurredAt = %v, want %v", tr.OccurredAt, now)
			}
			if r.action == ActionFail {
				if tr.Reason == nil || *tr.Reason != *r.reason {
					t.Errorf("transition.Reason = %v, want %v", tr.Reason, *r.reason)
				}
			} else if tr.Reason != nil {
				t.Errorf("transition.Reason = %v, want nil", tr.Reason)
			}

			// immutable fields untouched
			if got.ID != orig.ID || got.OrganizationID != orig.OrganizationID ||
				got.OwnerUserID != orig.OwnerUserID || got.Direction != orig.Direction ||
				!got.CreatedAt.Equal(orig.CreatedAt) {
				t.Errorf("identity fields changed: %+v", got)
			}
		})
	}
}

func TestNextVersionMismatch(t *testing.T) {
	for _, st := range []Status{StatusDialing, StatusRinging, StatusActive, StatusEnded} {
		c := newCall(DirectionOutbound, st)
		got, tr, err := Next(c, Command{Action: ActionEnd, ExpectedVersion: c.Version + 1}, now)
		if !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("%s: err = %v, want ErrVersionMismatch", st, err)
		}
		if got.Status != c.Status || got.Version != c.Version || got.EndedAt != nil {
			t.Errorf("%s: call changed: %+v", st, got)
		}
		if tr != (Transition{}) {
			t.Errorf("%s: transition = %+v, want zero value", st, tr)
		}
	}
}

func TestNextDoesNotMutateInput(t *testing.T) {
	c := newCall(DirectionOutbound, StatusRinging)
	_, _, err := Next(c, Command{Action: ActionConnect, ExpectedVersion: c.Version}, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != StatusRinging || c.Version != 3 || c.StartedAt != nil {
		t.Errorf("input mutated: %+v", c)
	}
}
