package tenancy_test

// The tenant organization model against a live database: creation is
// idempotent on the park claim, limits changes freeze/thaw per the product
// ruling (a quota freeze lifts itself, an admin freeze never does), the
// slot gate refuses a full or frozen tenant, and the playback limits lookup
// sees a freeze immediately (the cache is invalidated by every state
// change).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func testTenantPool(t *testing.T) (context.Context, *pgxpool.Pool, *tenancy.Store) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool, tenancy.NewStore(pool)
}

func seedTenantAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role)
		VALUES ($1, $2, 'x', 'user') RETURNING id`,
		name+"@tenant.test", name).Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func uniqueServiceID(t *testing.T, label string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s-%d", label, t.Name(), time.Now().UnixNano())
}

func TestTenantOrganizationLifecycle(t *testing.T) {
	ctx, pool, store := testTenantPool(t)
	serviceID := uniqueServiceID(t, "svc")

	created, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Acme Streams", ExternalOperatorID: "op-1", ExternalServiceID: serviceID,
		Slots: 2, Transcodes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Frozen {
		t.Fatalf("a fresh tenant organization must not be frozen: %+v", created)
	}

	// Idempotent on the park claim: a replayed fulfill job adopts, never mints.
	again, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Acme Streams (replay)", ExternalOperatorID: "op-1", ExternalServiceID: serviceID,
		Slots: 99, Transcodes: 99,
	})
	if err != nil || again.ID != created.ID || again.Slots != 2 {
		t.Fatalf("replayed create = (%+v, %v), want the original tenant unchanged", again, err)
	}

	// Slot gate: room for two admin members (an admin membership activates
	// the tenant), then full.
	if err := store.TenantSlotFree(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	memberOne := seedTenantAccount(t, ctx, pool, "member-one")
	if _, err := store.ProvisionTenantMembership(ctx, created.ID, memberOne, "admin"); err != nil {
		t.Fatalf("provision first member: %v", err)
	}
	memberTwo := seedTenantAccount(t, ctx, pool, "member-two")
	if _, err := store.ProvisionTenantMembership(ctx, created.ID, memberTwo, "user"); err != nil {
		t.Fatalf("provision second member: %v", err)
	}
	if err := store.TenantSlotFree(ctx, created.ID); !errors.Is(err, tenancy.ErrTenantSlotsExhausted) {
		t.Fatalf("full tenant slot gate = %v", err)
	}
	listed, err := store.ListTenantOrganizations(ctx)
	found := false
	for _, item := range listed {
		if item.ID == created.ID {
			found = true
			if item.SlotsUsed != 2 {
				t.Fatalf("listed slots_used = %d, want 2", item.SlotsUsed)
			}
		}
	}
	if err != nil || !found {
		t.Fatalf("list = (%+v, %v), want to find %s", listed, err, created.ID)
	}

	// Downgrade below usage: frozen IMMEDIATELY, reason quota.
	downgraded, err := store.UpdateTenantOrganizationLimits(ctx, created.ID, 1, 1)
	if err != nil || !downgraded.Frozen || downgraded.FrozenReason != tenancy.TenantFrozenReasonQuota {
		t.Fatalf("downgrade = (%+v, %v), want an immediate quota freeze", downgraded, err)
	}
	// A thaw while still over quota re-freezes: the ruling holds.
	thawed, err := store.SetTenantOrganizationFrozen(ctx, created.ID, false)
	if err != nil || !thawed.Frozen || thawed.FrozenReason != tenancy.TenantFrozenReasonQuota {
		t.Fatalf("thaw over quota = (%+v, %v), want still frozen", thawed, err)
	}
	// Upgrading back over usage lifts the QUOTA freeze automatically.
	upgraded, err := store.UpdateTenantOrganizationLimits(ctx, created.ID, 4, 2)
	if err != nil || upgraded.Frozen {
		t.Fatalf("upgrade = (%+v, %v), want thawed", upgraded, err)
	}
	// An ADMIN freeze survives a limits change; only thaw lifts it.
	if _, err := store.SetTenantOrganizationFrozen(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	afterLimits, err := store.UpdateTenantOrganizationLimits(ctx, created.ID, 5, 2)
	if err != nil || !afterLimits.Frozen || afterLimits.FrozenReason != tenancy.TenantFrozenReasonAdmin {
		t.Fatalf("admin freeze after limits = (%+v, %v), want frozen", afterLimits, err)
	}
	unfrozen, err := store.SetTenantOrganizationFrozen(ctx, created.ID, false)
	if err != nil || unfrozen.Frozen {
		t.Fatalf("admin thaw = (%+v, %v)", unfrozen, err)
	}

	// Teardown, in the order the wire contract requires the admin handler to
	// follow: the organization retires FIRST (this also clears the owner
	// reference — memberOne is the tenant's owner by now, and that FK is
	// RESTRICT, not CASCADE), THEN member accounts are deleted through the
	// caller's own user repository in production; a raw delete here stands
	// in for it.
	if err := store.DeleteTenantOrganization(ctx, created.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []int{memberOne, memberTwo}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTenantOrganization(ctx, created.ID); !errors.Is(err, tenancy.ErrTenantOrganizationNotFound) {
		t.Fatalf("second delete = %v, want ErrTenantOrganizationNotFound", err)
	}
	if _, err := store.GetTenantOrganization(ctx, created.ID); !errors.Is(err, tenancy.ErrTenantOrganizationNotFound) {
		t.Fatalf("get after delete = %v, want ErrTenantOrganizationNotFound", err)
	}

	// A canceled-then-re-sold park service — the identical name AND the
	// identical external_service_id — must become a brand new tenant, not
	// collide with the retired row it replaces. Name and service id derive
	// the same slug deterministically, so this also pins that retiring a
	// tenant frees its slug, not just its external_service_id.
	recreated, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Acme Streams", ExternalOperatorID: "op-1", ExternalServiceID: serviceID,
		Slots: 3, Transcodes: 1,
	})
	if err != nil {
		t.Fatalf("recreate after delete = %v, want a fresh tenant, not a collision", err)
	}
	if recreated.ID == created.ID {
		t.Fatalf("recreate after delete reused the retired organization's id")
	}
	t.Cleanup(func() { _ = store.DeleteTenantOrganization(context.Background(), recreated.ID) })
}

func TestTenantLimitsForUserSeesFreezesImmediately(t *testing.T) {
	ctx, pool, store := testTenantPool(t)
	serviceID := uniqueServiceID(t, "svc")

	created, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Freezer", ExternalOperatorID: "op-1", ExternalServiceID: serviceID,
		Slots: 3, Transcodes: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	accountID := seedTenantAccount(t, ctx, pool, "member-frozen")
	// t.Cleanup is LIFO: registered after seedTenantAccount's own cleanup,
	// so this runs FIRST — clearing the organization's owner reference
	// before that cleanup tries to delete the account. An account still
	// named as an organization's owner cannot be deleted (RESTRICT), and
	// this one becomes exactly that the moment it holds an admin
	// membership, below.
	t.Cleanup(func() { _ = store.DeleteTenantOrganization(context.Background(), created.ID) })
	if _, err := store.ProvisionTenantMembership(ctx, created.ID, accountID, "admin"); err != nil {
		t.Fatal(err)
	}

	limits, err := store.TenantLimitsForUser(ctx, accountID)
	if err != nil || limits.TenantID == "" || limits.MaxTranscodes != 4 || limits.Frozen {
		t.Fatalf("limits = (%+v, %v)", limits, err)
	}
	// The lookup is cached, but a freeze invalidates: playback admission
	// must not serve a frozen tenant for another TTL.
	if _, err := store.SetTenantOrganizationFrozen(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	limits, err = store.TenantLimitsForUser(ctx, accountID)
	if err != nil || !limits.Frozen {
		t.Fatalf("limits after freeze = (%+v, %v), want frozen NOW", limits, err)
	}

	// An account with no tenant organization membership answers the zero value.
	plainID := seedTenantAccount(t, ctx, pool, "plain-user")
	limits, err = store.TenantLimitsForUser(ctx, plainID)
	if err != nil || limits.TenantID != "" {
		t.Fatalf("untenanted limits = (%+v, %v)", limits, err)
	}
}
