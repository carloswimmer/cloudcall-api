package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Stable demo identifiers shared by the seed, API and tests.
var (
	OrganizationID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	DemoUserID     = uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	Colleague1ID   = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	Colleague2ID   = uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	Contact1ID     = uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
)

// Seed inserts the demo organization, users and contact. It is idempotent:
// existing rows (matched by primary key, or by (organization_id, phone) for the
// contact) are left untouched, so presence changes and re-created contacts
// survive restarts.
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

	users := []struct {
		id        uuid.UUID
		name      string
		extension string
	}{
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

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO contacts (id, organization_id, name, phone, email, created_at, updated_at)
		VALUES ($1, $2, 'Ada Lovelace', '+442071838750', 'ada@example.com', now(), now())
		ON CONFLICT DO NOTHING`, Contact1ID, OrganizationID); err != nil {
		return fmt.Errorf("seed contact: %w", err)
	}
	return tx.Commit()
}
