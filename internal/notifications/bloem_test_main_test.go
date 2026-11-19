package notifications

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// TestMain runs the package on its own prepared database, with an active,
// owned organization for the profiles Silo's tests copy into a private schema.
// Bloem only delivers a notification to a profile of an active organization
// (deliveryAccessPredicate). A copied profile takes the fixture shim's
// placeholder organization, so this creates that organization here, on this
// package's database only, and those tests send as in production.
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(func() int {
		if err := createPlaceholderOrganization(); err != nil {
			fmt.Fprintf(os.Stderr, "create the placeholder organization: %v\n", err)
			return 1
		}
		return m.Run()
	}))
}

func createPlaceholderOrganization() error {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	// An active organization has an owner, as after setup: one account made
	// for this suite.
	var owner int
	if err := conn.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role, enabled, is_owner)
		VALUES ('bloem-notifications-suite-owner', 'notifications-suite-owner@example.invalid', 'unused', 'admin', true, true)
		RETURNING id`).Scan(&owner); err != nil {
		return fmt.Errorf("create the suite owner: %w", err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO organizations (id, slug, name, status, owner_account_id)
		VALUES (public.bloem_fixture_media_request_placeholder_organization(),
			'bloem-notifications-suite', 'Notifications suite', 'active', $1)`, owner)
	return err
}
