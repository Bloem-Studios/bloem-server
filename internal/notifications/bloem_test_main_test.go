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

// TestMain runs the package on its own prepared database, set up as a server
// is after setup. Bloem only delivers a notification to a profile of an
// active organization (deliveryAccessPredicate), and a fresh test database
// has none:
//   - the default organization is made active, owned by account 1, which
//     gets the profile "default" Silo's tests write deliveries for;
//   - the fixture shim's placeholder organization, which the profiles Silo's
//     tests copy into a private schema take, is created active.
//
// Both happen on this package's database only.
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(func() int {
		if err := prepareSuiteOrganizations(); err != nil {
			fmt.Fprintf(os.Stderr, "prepare the suite organizations: %v\n", err)
			return 1
		}
		return m.Run()
	}))
}

func prepareSuiteOrganizations() error {
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
	// Account 1 owns both organizations, as the first account does after setup.
	const owner = 1
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role, enabled, is_owner)
		OVERRIDING SYSTEM VALUE
		VALUES (1, 'bloem-notifications-suite-owner', 'notifications-suite-owner@example.invalid', 'unused', 'admin', true, true)`); err != nil {
		return fmt.Errorf("create the suite owner: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT setval(pg_get_serial_sequence('public.users', 'id'), GREATEST((SELECT max(id) FROM users), 1))`); err != nil {
		return fmt.Errorf("move the account ids past the owner: %w", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE organizations SET owner_account_id = $1, status = 'active'
		WHERE is_default AND status = 'initializing'`, owner); err != nil {
		return fmt.Errorf("activate the default organization: %w", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO user_profiles (id, user_id, name, is_primary) VALUES ('default', 1, 'Default', true)`); err != nil {
		return fmt.Errorf("create the default profile: %w", err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO organizations (id, slug, name, status, owner_account_id)
		VALUES (public.bloem_fixture_media_request_placeholder_organization(),
			'bloem-notifications-suite', 'Notifications suite', 'active', $1)`, owner)
	return err
}
