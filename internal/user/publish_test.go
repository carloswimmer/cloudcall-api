package user_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"cloudcall/internal/event"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

type recorder struct {
	mu     sync.Mutex
	events []event.Envelope
}

func (r *recorder) Publish(e event.Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) all() []event.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Envelope(nil), r.events...)
}

func TestSetPresencePublishesFullUserAfterCommit(t *testing.T) {
	sqlDB, ctx := openDB(t)
	orgID, userID := uuid.New(), uuid.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Publish Org', '#1F4E79', 'Europe/Berlin')`,
		orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Publish User', '903', 'available', 1)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	})

	rec := &recorder{}
	store := &user.Store{DB: sqlDB, Publisher: rec}

	if _, err := store.SetPresence(ctx, orgID, userID, user.PresenceOffline, 9); !errors.Is(err, user.ErrVersionMismatch) {
		t.Fatalf("err = %v", err)
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("stale write published %+v", got)
	}

	if _, err := store.SetPresence(ctx, orgID, userID, user.PresenceOffline, 1); err != nil {
		t.Fatal(err)
	}
	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("events = %+v", got)
	}
	ev := got[0]
	if ev.Type != "presence.updated" || ev.EventID == "" || ev.OccurredAt.IsZero() ||
		ev.EntityID == nil || *ev.EntityID != userID.String() || ev.EntityVersion == nil || *ev.EntityVersion != 2 {
		t.Fatalf("envelope = %+v", ev)
	}
	var u user.User
	if err := json.Unmarshal(ev.Payload, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != userID || u.Name != "Publish User" || u.Extension != "903" || u.Presence != user.PresenceOffline || u.Version != 2 {
		t.Fatalf("payload = %s", ev.Payload)
	}
}
