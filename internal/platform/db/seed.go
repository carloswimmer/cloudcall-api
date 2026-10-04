package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Stable demo identifiers shared by the seed, API and tests.
const (
	OrganizationID = "11111111-1111-4111-8111-111111111111"
	DemoUserID     = "22222222-2222-4222-8222-222222222201"
	Colleague1ID   = "22222222-2222-4222-8222-222222222202"
	Colleague2ID   = "22222222-2222-4222-8222-222222222203"
	// Contact1ID is reserved for the contacts seed (no contact is inserted yet).
	Contact1ID = "33333333-3333-4333-8333-333333333301"
)

// Seed inserts the demo organization and users. It is idempotent: existing rows
// (matched by primary key) are left untouched, so presence changes survive restarts.
func Seed(ctx context.Context, sqlDB *sql.DB) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO organizations (id, name, brand_color, timezone)
		VALUES ($1, 'Northwind', '#1F4E79', 'Europe/Berlin')
		ON CONFLICT (id) DO NOTHING`, OrganizationID); err != nil {
		return fmt.Errorf("seed organization: %w", err)
	}

	users := []struct{ id, name, extension string }{
		{DemoUserID, "Alex Rivera", "100"},
		{Colleague1ID, "Jordan Lee", "101"},
		{Colleague2ID, "Sam Okafor", "102"},
	}
	for _, u := range users {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, organization_id, name, extension, presence, version)
			VALUES ($1, $2, $3, $4, 'available', 1)
			ON CONFLICT (id) DO NOTHING`, u.id, OrganizationID, u.name, u.extension); err != nil {
			return fmt.Errorf("seed user %s: %w", u.extension, err)
		}
	}
	return tx.Commit()
}
