package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"

	"cloudcall/internal/platform/db"
)

func cleanupLegacyDemoSeed(t *testing.T, sqlDB *sql.DB, ctx context.Context) {
	t.Helper()
	legacyOrg := "11111111-1111-4111-8111-111111111111"
	legacyUsers := []string{
		"22222222-2222-4222-8222-222222222201",
		"22222222-2222-4222-8222-222222222202",
		"22222222-2222-4222-8222-222222222203",
	}
	for _, uid := range legacyUsers {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", uid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM organizations WHERE id = $1", legacyOrg); err != nil {
		t.Fatal(err)
	}
}

func TestSeedIsIdempotentAndMatchesDemoData(t *testing.T) {
	sqlDB, ctx := openTestDB(t)
	cleanupLegacyDemoSeed(t, sqlDB, ctx)

	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}

	var orgs, users int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM organizations WHERE id = $1", db.OrganizationID).Scan(&orgs); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM users WHERE organization_id = $1", db.OrganizationID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if orgs != 1 || users != 3 {
		t.Fatalf("orgs=%d users=%d", orgs, users)
	}

	var name, brand, tz string
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT name, brand_color, timezone FROM organizations WHERE id = $1", db.OrganizationID).
		Scan(&name, &brand, &tz); err != nil {
		t.Fatal(err)
	}
	if name != "Northwind" || brand != "#1F4E79" || tz != "Europe/Berlin" {
		t.Fatalf("org %q %q %q", name, brand, tz)
	}

	var uname, ext, presence string
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT name, extension, presence FROM users WHERE id = $1", db.DemoUserID).
		Scan(&uname, &ext, &presence); err != nil {
		t.Fatal(err)
	}
	if uname != "Alex Rivera" || ext != "100" || presence != "available" {
		t.Fatalf("demo user %q %q %q", uname, ext, presence)
	}

	for _, id := range []uuid.UUID{db.Colleague1ID, db.Colleague2ID} {
		var n int
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT count(*) FROM users WHERE id = $1 AND organization_id = $2", id, db.OrganizationID).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("colleague %s rows: %d", id, n)
		}
	}
}

func TestSeedDoesNotResetExistingPresence(t *testing.T) {
	sqlDB, ctx := openTestDB(t)
	cleanupLegacyDemoSeed(t, sqlDB, ctx)

	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		"UPDATE users SET presence = 'busy', version = version + 1 WHERE id = $1", db.Colleague1ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("UPDATE users SET presence = 'available', version = 1 WHERE id = $1", db.Colleague1ID)
	})
	if err := db.Seed(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	var presence string
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT presence FROM users WHERE id = $1", db.Colleague1ID).Scan(&presence); err != nil {
		t.Fatal(err)
	}
	if presence != "busy" {
		t.Fatalf("presence reset to %q", presence)
	}
}
