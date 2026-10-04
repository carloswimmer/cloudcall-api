package user_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloudcall/internal/call"
	"cloudcall/internal/platform/httpx"
	"cloudcall/internal/user"

	"github.com/google/uuid"
)

func TestPatchPresenceLockedDuringLiveCall(t *testing.T) {
	sqlDB, ctx := openDB(t)
	orgID, userID := uuid.New(), uuid.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Lock Org', '#1F4E79', 'Europe/Berlin')`,
		orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version)
		 VALUES ($1, $2, 'Lock User', '902', 'available', 1)`, userID, orgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM call_transitions WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1)`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, orgID)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	})

	users := &user.Store{DB: sqlDB}
	calls := &call.Store{DB: sqlDB, Users: users}
	mux := http.NewServeMux()
	user.NewHandler(users, orgID, userID).WithCallLock(calls).Register(mux)
	srv := httptest.NewServer(httpx.Middleware("http://localhost:4200", mux))
	t.Cleanup(srv.Close)
	url := srv.URL + "/api/v1/me/presence"

	c, err := calls.Create(ctx, orgID, userID, call.DirectionOutbound, nil, "Ada Lovelace", "+442071838750")
	if err != nil {
		t.Fatal(err)
	}

	// The call bumped the user to version 2 (busy).
	res, raw := do(t, http.MethodPatch, url, `{"presence":"offline","expectedVersion":2}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var e errBody
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != "presence_locked" || e.RequestID == "" {
		t.Fatalf("error %+v", e)
	}
	u, err := users.Get(ctx, orgID, userID)
	if err != nil || u.Presence != user.PresenceBusy || u.Version != 2 {
		t.Fatalf("user changed: %+v (%v)", u, err)
	}

	if _, err := calls.Apply(ctx, orgID, c.ID, call.Command{Action: call.ActionEnd, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	// Ending restored presence and bumped the version to 3.
	res, raw = do(t, http.MethodPatch, url, `{"presence":"offline","expectedVersion":3}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("after call: status %d: %s", res.StatusCode, raw)
	}
}
