package orglive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

// These tests run the real migration, so the triggers are the shipped ones, and
// the real listener against a real Postgres. They need SILO_TEST_DATABASE_URL
// to name a server where the test may create and drop databases.

type orgLiveDB struct {
	t        *testing.T
	pool     *pgxpool.Pool
	orgA     uuid.UUID
	orgB     uuid.UUID
	ownerA   int
	ownerB   int
	accounts int
}

func newOrgLiveDB(t *testing.T) *orgLiveDB {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_orglive_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	db := &orgLiveDB{t: t, pool: pool}
	db.ownerA = db.user("owner-a")
	db.ownerB = db.user("owner-b")
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&db.orgA); err != nil {
		t.Fatal(err)
	}
	db.exec(`UPDATE organizations SET status='active', owner_account_id=$2 WHERE id=$1`, db.orgA, db.ownerA)
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug,name,status,owner_account_id) VALUES ('live-b','Live B','active',$1) RETURNING id`, db.ownerB).Scan(&db.orgB); err != nil {
		t.Fatal(err)
	}
	return db
}

func (d *orgLiveDB) exec(query string, args ...any) {
	d.t.Helper()
	if _, err := d.pool.Exec(context.Background(), query, args...); err != nil {
		d.t.Fatalf("%s: %v", query, err)
	}
}

func (d *orgLiveDB) user(name string) int {
	d.t.Helper()
	d.accounts++
	var id int
	if err := d.pool.QueryRow(context.Background(), `INSERT INTO users (email,username,password_hash,role,enabled) VALUES ($1,$2,'hash','user',true) RETURNING id`,
		fmt.Sprintf("%s-%d@example.test", name, d.accounts), fmt.Sprintf("%s-%d", name, d.accounts)).Scan(&id); err != nil {
		d.t.Fatal(err)
	}
	return id
}

func (d *orgLiveDB) membership(org uuid.UUID, account int, role string) {
	d.t.Helper()
	d.exec(`INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active',$3)`, org, account, role)
}

// listen starts the real listener on its own connection and waits for it.
func (d *orgLiveDB) listen(t *testing.T) *Bus {
	t.Helper()
	bus := NewBus()
	listener := NewListener(bus, d.pool.Config().ConnConfig, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); listener.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitHealthy(t, bus, true)
	return bus
}

func collect(t *testing.T, m *Mailbox, want int) []Frame {
	t.Helper()
	return waitFrames(t, m, want)
}

func idsOf(frames []Frame) map[string]Kind {
	out := map[string]Kind{}
	for _, f := range frames {
		out[string(f.Channel)+"/"+f.Change.ID] = f.Change.Kind
	}
	return out
}

func TestTriggersSignalTheOrganizationThatOwnsTheChange(t *testing.T) {
	db := newOrgLiveDB(t)
	bus := db.listen(t)
	a, b := subscribed(ChannelPeople, ChannelActivity), subscribed(ChannelPeople, ChannelActivity)
	if _, err := bus.Register(db.orgA, 1, a); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Register(db.orgB, 2, b); err != nil {
		t.Fatal(err)
	}

	// A person joins organization A: a membership appears.
	member := db.user("member")
	db.membership(db.orgA, member, "user")
	frames := collect(t, a, 1)
	if got := frames[0]; got.Channel != ChannelPeople || got.Change.ID != strconv.Itoa(member) || got.Change.Kind != KindCreated || got.Change.Rev == "" {
		t.Fatalf("join: %+v", got)
	}
	joinRev := frames[0].Change.Rev

	// Status and role changes are updates with a new revision.
	db.exec(`UPDATE organization_memberships SET status='suspended', security_revision=security_revision+1, updated_at=now() WHERE organization_id=$1 AND account_id=$2`, db.orgA, member)
	frames = collect(t, a, 1)
	if got := frames[0]; got.Change.Kind != KindUpdated || got.Change.ID != strconv.Itoa(member) || got.Change.Rev == joinRev {
		t.Fatalf("status change: %+v (join rev %s)", got, joinRev)
	}
	db.exec(`UPDATE organization_memberships SET legacy_role='admin', updated_at=now() WHERE organization_id=$1 AND account_id=$2`, db.orgA, member)
	if got := collect(t, a, 1)[0]; got.Change.Kind != KindUpdated {
		t.Fatalf("role change: %+v", got)
	}

	// A removal is a delete.
	db.exec(`DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, db.orgA, member)
	if got := collect(t, a, 1)[0]; got.Change.Kind != KindDeleted || got.Change.ID != strconv.Itoa(member) {
		t.Fatalf("removal: %+v", got)
	}

	// Activity: an administrative audit entry about a person is a new activity
	// entry and marks the person changed; an entitlement entry likewise.
	target := db.user("target")
	var auditID int64
	if err := db.pool.QueryRow(context.Background(), `INSERT INTO admin_audit_events
		(actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome)
		VALUES ($1,'organization_admin','organization','profile.renamed','membership','x',$2,$3,1,2,'success') RETURNING id`,
		db.ownerA, db.orgA, strconv.Itoa(target)).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	got := idsOf(collect(t, a, 2))
	if got["org.activity/lifecycle:"+strconv.FormatInt(auditID, 10)] != KindCreated || got["org.people/"+strconv.Itoa(target)] != KindUpdated {
		t.Fatalf("audit entry: %v", got)
	}
	var entitlementID int64
	if err := db.pool.QueryRow(context.Background(), `INSERT INTO entitlement_audit_events (actor_account_id,action,organization_id,target_account_id) VALUES ($1,'apply',$2,$3) RETURNING id`,
		db.ownerA, db.orgA, target).Scan(&entitlementID); err != nil {
		t.Fatal(err)
	}
	got = idsOf(collect(t, a, 2))
	if got["org.activity/entitlement:"+strconv.FormatInt(entitlementID, 10)] != KindCreated || got["org.people/"+strconv.Itoa(target)] != KindUpdated {
		t.Fatalf("entitlement entry: %v", got)
	}

	// An entitlement event that names no organization is nobody's activity.
	db.exec(`INSERT INTO entitlement_audit_events (actor_account_id,action,organization_id,target_account_id) VALUES ($1,'platform.template',NULL,NULL)`, db.ownerA)

	// A change that rolls back never signals.
	tx, err := db.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active','user')`, db.orgA, db.user("ghost")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Organization B saw none of organization A's changes.
	expectQuiet(t, a, "organization A")
	expectQuiet(t, b, "organization B")

	// And B's own changes reach B alone.
	other := db.user("b-member")
	db.membership(db.orgB, other, "user")
	if got := collect(t, b, 1)[0]; got.Change.ID != strconv.Itoa(other) || got.Change.Kind != KindCreated {
		t.Fatalf("organization B join: %+v", got)
	}
	expectQuiet(t, a, "organization A")
}

func TestTriggersNeverFailTheWriteTheySignal(t *testing.T) {
	db := newOrgLiveDB(t)
	// A signal function that blows up must not take the membership write with it.
	db.exec(`CREATE OR REPLACE FUNCTION public.bloem_org_live_notify(channel text, organization uuid, item_id text, revision text, kind text)
		RETURNS void LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'signal failed'; END; $$`)
	account := db.user("resilient")
	db.membership(db.orgA, account, "user")
	db.exec(`UPDATE organization_memberships SET status='suspended' WHERE organization_id=$1 AND account_id=$2`, db.orgA, account)
	db.exec(`INSERT INTO entitlement_audit_events (actor_account_id,action,organization_id,target_account_id) VALUES ($1,'apply',$2,$3)`, db.ownerA, db.orgA, account)
	db.exec(`DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, db.orgA, account)
	var left int
	if err := db.pool.QueryRow(context.Background(), `SELECT count(*) FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, db.orgA, account).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the write did not complete: %d rows left, %v", left, err)
	}
}

func TestListenerRecoversFromALostConnection(t *testing.T) {
	db := newOrgLiveDB(t)
	bus := db.listen(t)
	m := subscribed(ChannelPeople)
	if _, err := bus.Register(db.orgA, 1, m); err != nil {
		t.Fatal(err)
	}

	// Kill the listener's backend, as a failover or a network drop would.
	var killed int
	if err := db.pool.QueryRow(context.Background(), `SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE datname=current_database() AND query LIKE 'LISTEN %' AND pid<>pg_backend_pid()`).Scan(&killed); err != nil || killed != 1 {
		t.Fatalf("terminated %d listener backends: %v", killed, err)
	}
	select {
	case <-m.Done():
		if m.Reason() != ReasonFeedDown {
			t.Fatalf("reason = %v", m.Reason())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a socket stayed open after the listener's connection died")
	}
	waitHealthy(t, bus, true) // reconnects with backoff

	// Signals flow again for a connection registered after the recovery.
	again := subscribed(ChannelPeople)
	if _, err := bus.Register(db.orgA, 1, again); err != nil {
		t.Fatal(err)
	}
	account := db.user("after-recovery")
	db.membership(db.orgA, account, "user")
	if got := collect(t, again, 1)[0]; got.Change.ID != strconv.Itoa(account) {
		t.Fatalf("after recovery: %+v", got)
	}
}
