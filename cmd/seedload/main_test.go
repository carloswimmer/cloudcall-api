package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"cloudcall/internal/platform/db"

	"github.com/google/uuid"
)

func TestSeedInsertsTerminalHistoryAndIsRepeatable(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sqlDB, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}

	org, owner := uuid.New(), uuid.New()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO organizations (id, name, brand_color, timezone) VALUES ($1, 'Seedload Org', '#1F4E79', 'UTC')`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO users (id, organization_id, name, extension, presence, version) VALUES ($1, $2, 'Seed Owner', '930', 'available', 1)`,
		owner, org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM calls WHERE organization_id = $1`, org)
		_, _ = sqlDB.Exec(`DELETE FROM contacts WHERE organization_id = $1`, org)
		_, _ = sqlDB.Exec(`DELETE FROM users WHERE organization_id = $1`, org)
		_, _ = sqlDB.Exec(`DELETE FROM organizations WHERE id = $1`, org)
	})
	// A real contact outside the lab range must survive every run.
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO contacts (id, organization_id, name, phone, created_at, updated_at) VALUES ($1, $2, 'Keep Me', '+442071838750', now(), now())`,
		uuid.New(), org); err != nil {
		t.Fatal(err)
	}

	// Batch size 7 does not divide 40 or 130, so the last batch is partial.
	for run := 1; run <= 2; run++ {
		if err := seed(ctx, sqlDB, org, owner, 40, 130, 7); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if got := count(t, ctx, sqlDB, `SELECT count(*) FROM contacts WHERE organization_id = $1 AND phone LIKE '+1555%'`, org); got != 40 {
			t.Fatalf("run %d: %d lab contacts, want 40", run, got)
		}
		if got := count(t, ctx, sqlDB, `SELECT count(*) FROM contacts WHERE organization_id = $1 AND phone = '+442071838750'`, org); got != 1 {
			t.Fatalf("run %d: unrelated contact lost", run)
		}
		if got := count(t, ctx, sqlDB, `SELECT count(*) FROM calls WHERE organization_id = $1`, org); got != 130 {
			t.Fatalf("run %d: %d calls, want 130", run, got)
		}
		if got := count(t, ctx, sqlDB,
			`SELECT count(*) FROM calls WHERE organization_id = $1 AND status NOT IN ('ended','rejected','missed','failed')`, org); got != 0 {
			t.Fatalf("run %d: %d non-terminal calls", run, got)
		}
		if got := count(t, ctx, sqlDB,
			`SELECT count(DISTINCT status) FROM calls WHERE organization_id = $1`, org); got != 4 {
			t.Fatalf("run %d: %d distinct statuses, want 4", run, got)
		}
		if got := count(t, ctx, sqlDB,
			`SELECT count(*) FROM calls WHERE organization_id = $1 AND status = 'ended' AND (started_at IS NULL OR ended_at IS NULL OR ended_at < started_at)`, org); got != 0 {
			t.Fatalf("run %d: %d ended calls with bad timestamps", run, got)
		}
		if got := count(t, ctx, sqlDB,
			`SELECT count(*) FROM calls WHERE organization_id = $1 AND owner_user_id <> $2`, org, owner); got != 0 {
			t.Fatalf("run %d: calls owned by someone else", run)
		}
	}
}

func count(t *testing.T, ctx context.Context, sqlDB *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
