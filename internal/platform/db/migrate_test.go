package db_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"cloudcall/internal/platform/db"
)

func openTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	sqlDB, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB, ctx
}

func TestMigrateAppliesOnceAndIsIdempotent(t *testing.T) {
	sqlDB, ctx := openTestDB(t)

	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM schema_migrations WHERE version = '001'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("schema_migrations rows for 001: %d", n)
	}

	for _, table := range []string{"organizations", "users"} {
		var exists bool
		if err := sqlDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("table %s missing", table)
		}
	}
}
