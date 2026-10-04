package call_test

import (
	"errors"
	"sync"
	"testing"

	"cloudcall/internal/call"
)

// Create locks user then call; Apply to a terminal status must take the same
// order or the two deadlock (SQLSTATE 40P01). Either side may win the race, but
// neither may fail with anything other than the documented ErrActiveCallExists.
func TestConcurrentCreateAndTerminalApplyDoNotDeadlock(t *testing.T) {
	e := newStoreEnv(t)

	for i := 0; i < 40; i++ {
		first := e.createOutbound(t)

		var (
			wg        sync.WaitGroup
			start     = make(chan struct{})
			createErr error
			applyErr  error
			created   call.Call
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			created, createErr = e.store.Create(e.ctx, e.org, e.owner, call.DirectionOutbound, nil, "Racer", "+442071838750")
		}()
		go func() {
			defer wg.Done()
			<-start
			_, applyErr = e.store.Apply(e.ctx, e.org, first.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1})
		}()
		close(start)
		wg.Wait()

		if applyErr != nil {
			t.Fatalf("iteration %d: apply failed: %v", i, applyErr)
		}
		if createErr != nil && !errors.Is(createErr, call.ErrActiveCallExists) {
			t.Fatalf("iteration %d: create failed (deadlock?): %v", i, createErr)
		}

		// The owner is busy exactly when the racing Create won.
		u := e.ownerUser(t)
		switch {
		case createErr == nil:
			if string(u.Presence) != "busy" {
				t.Fatalf("iteration %d: create won but presence = %q", i, u.Presence)
			}
			if _, err := e.store.Apply(e.ctx, e.org, created.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
				t.Fatalf("iteration %d: end winner: %v", i, err)
			}
		default:
			if string(u.Presence) != "available" {
				t.Fatalf("iteration %d: create lost but presence = %q", i, u.Presence)
			}
		}
		if has, err := e.store.HasNonTerminal(e.ctx, e.org, e.owner); err != nil || has {
			t.Fatalf("iteration %d: leftover non-terminal call (has=%v err=%v)", i, has, err)
		}
	}
}
