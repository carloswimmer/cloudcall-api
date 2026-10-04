// Command seedload fills the demo organization with lab data (1_000 contacts and
// 10_000 terminal historical calls) to try pagination, filters and dashboard
// queries on a realistic volume.
//
// It is a manual tool: run it once with `go run ./cmd/seedload`. Never run it on
// API startup (the API only runs the small idempotent demo seed).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"cloudcall/internal/platform/config"
	"cloudcall/internal/platform/db"

	"github.com/google/uuid"
)

const (
	labContacts = 1_000
	labCalls    = 10_000
	batchSize   = 500

	// labPhonePrefix marks every row this command owns. Running the command again
	// deletes those rows first, so the result is the same after any number of runs.
	labPhonePrefix = "+1555"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sqlDB, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := db.Seed(ctx, sqlDB); err != nil {
		log.Fatalf("seed: %v", err)
	}
	start := time.Now()
	if err := seed(ctx, sqlDB, db.OrganizationID, db.DemoUserID, labContacts, labCalls, batchSize); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded %d contacts and %d calls in %s", labContacts, labCalls, time.Since(start).Round(time.Millisecond))
}

// seed replaces the lab rows of the organization (phones starting with +1555) with
// nContacts contacts and nCalls terminal calls owned by ownerID, in one
// transaction, inserting batch rows per statement.
func seed(ctx context.Context, sqlDB *sql.DB, orgID, ownerID uuid.UUID, nContacts, nCalls, batch int) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := deleteLabRows(ctx, tx, orgID); err != nil {
		return err
	}

	type labContact struct {
		id          uuid.UUID
		name, phone string
	}
	contacts := make([]labContact, nContacts)
	for i := range contacts {
		contacts[i] = labContact{
			id:    uuid.New(),
			name:  fmt.Sprintf("Lab Contact %04d", i+1),
			phone: fmt.Sprintf("%s%07d", labPhonePrefix, i+1),
		}
	}
	now := time.Now().UTC()
	if err := insertBatches(ctx, tx, len(contacts), batch, 7,
		`INSERT INTO contacts (id, organization_id, name, phone, email, created_at, updated_at) VALUES `,
		func(i int) []any {
			c := contacts[i]
			return []any{c.id, orgID, c.name, c.phone, fmt.Sprintf("lab%04d@example.com", i+1), now, now}
		}); err != nil {
		return fmt.Errorf("insert contacts: %w", err)
	}

	// One call every 12 minutes going back from an hour ago (10_000 calls ~ 83 days).
	// Out of every 10 calls: 6 ended, 2 missed, 1 rejected, 1 failed.
	base := now.Add(-time.Hour)
	if err := insertBatches(ctx, tx, nCalls, batch, 13,
		`INSERT INTO calls (id, organization_id, owner_user_id, contact_id, peer_name_snapshot, peer_phone_snapshot,
			direction, status, version, created_at, started_at, ended_at, failure_reason) VALUES `,
		func(i int) []any {
			var contactID any
			name, phone := "Lab Caller", fmt.Sprintf("%s%07d", labPhonePrefix, 9_000_000+i)
			if nContacts > 0 {
				c := contacts[i%nContacts]
				contactID, name, phone = c.id, c.name, c.phone
			}
			created := base.Add(-time.Duration(i) * 12 * time.Minute)
			direction, status := "outbound", "ended"
			var started, ended, reason any
			version := 1
			switch i % 10 {
			case 6, 7: // ringing -> missed (ring timeout)
				direction, status, version = "inbound", "missed", 2
				ended, reason = created.Add(30*time.Second), "ring_timeout"
			case 8: // ringing -> rejected
				direction, status, version = "inbound", "rejected", 2
				ended = created.Add(5 * time.Second)
			case 9: // dialing -> failed
				status, version = "failed", 2
				ended, reason = created.Add(8*time.Second), "network_error"
			default: // dialing/ringing -> active -> ended
				if (i/10)%2 == 1 {
					direction = "inbound"
				}
				s := created.Add(6 * time.Second)
				started, ended, version = s, s.Add(time.Duration(30+(i*37)%570)*time.Second), 3
				if direction == "outbound" {
					version = 4
				}
			}
			return []any{uuid.New(), orgID, ownerID, contactID, name, phone, direction, status, version, created, started, ended, reason}
		}); err != nil {
		return fmt.Errorf("insert calls: %w", err)
	}
	return tx.Commit()
}

func deleteLabRows(ctx context.Context, tx *sql.Tx, orgID uuid.UUID) error {
	like := labPhonePrefix + "%"
	for _, q := range []string{
		`DELETE FROM call_notes WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1 AND peer_phone_snapshot LIKE $2)`,
		`DELETE FROM call_transitions WHERE call_id IN (SELECT id FROM calls WHERE organization_id = $1 AND peer_phone_snapshot LIKE $2)`,
		`DELETE FROM calls WHERE organization_id = $1 AND peer_phone_snapshot LIKE $2`,
		`DELETE FROM contacts WHERE organization_id = $1 AND phone LIKE $2`,
	} {
		if _, err := tx.ExecContext(ctx, q, orgID, like); err != nil {
			return fmt.Errorf("delete lab rows: %w", err)
		}
	}
	return nil
}

// insertBatches runs multi-row INSERTs of at most batch rows. row(i) returns the
// width values of row i.
func insertBatches(ctx context.Context, tx *sql.Tx, total, batch, width int, insert string, row func(i int) []any) error {
	for from := 0; from < total; from += batch {
		to := min(from+batch, total)
		var sb strings.Builder
		sb.WriteString(insert)
		args := make([]any, 0, (to-from)*width)
		for i := from; i < to; i++ {
			if i > from {
				sb.WriteByte(',')
			}
			sb.WriteByte('(')
			for j := 0; j < width; j++ {
				if j > 0 {
					sb.WriteByte(',')
				}
				fmt.Fprintf(&sb, "$%d", len(args)+j+1)
			}
			sb.WriteByte(')')
			args = append(args, row(i)...)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}
