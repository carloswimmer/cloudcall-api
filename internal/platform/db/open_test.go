package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"cloudcall/internal/platform/db"
)

func TestOpenPingsAndSelects(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sqlDB, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	var one int
	if err := sqlDB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatal(err)
	}
	if one != 1 {
		t.Fatalf("got %d", one)
	}
}
