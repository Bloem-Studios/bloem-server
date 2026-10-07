//go:build integration

package nativestorage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeFixLibraries(pool *pgxpool.Pool, queue nativestorage.NativeLibraryQueue) *nativestorage.LibraryManagement {
	return nativestorage.NewLibraryManagement(pool, catalog.NewFolderRepository(pool), sections.NewRepository(pool), resourcetenancy.NewStore(pool), queue)
}
func nativeFixCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Errorf("want %s; got %T %v", code, err, err)
	}
}
func nativeFixRows(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	result := ""
	for _, table := range []string{"media_folders", "bloem_native_libraries", "library_collection_groups", "bloem_storage_sources", "bloem_storage_bindings", "plugin_installations", "bloem_storage_installations", "scan_runs"} {
		var rows string
		err := pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(j ORDER BY j::text)::text,'[]') FROM (SELECT to_jsonb(t) j FROM "+pgx.Identifier{table}.Sanitize()+" t) x").Scan(&rows)
		if err != nil {
			t.Fatal(err)
		}
		result += table + ":" + rows + "\n"
	}
	return result
}
func nativeFixGrant(t *testing.T, pool *pgxpool.Pool, organization auth.AdminContextClaims, source nativestorage.SourceView) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `SELECT e.id FROM organization_entitlements e JOIN bloem_storage_sources s ON s.owner_id=e.root_owner_id
 WHERE s.key=$1 AND e.organization_id=$2 AND e.plugin_installation_id=$3 AND e.root_kind='plugin_installation'
 AND e.entitlement_kind='plugin_availability' AND e.status='active' AND e.security_revision>0`, source.SourceKey, organization.OrganizationID, *source.InstallationID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func nativeFixBlock(t *testing.T, pool *pgxpool.Pool, table string, id uuid.UUID) (pgx.Tx, uint32) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var actual string
	if err = tx.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != pool.Config().ConnConfig.Database {
		t.Fatal("blocker actual owned pool identity differs")
	}
	var locked uuid.UUID
	if err = tx.QueryRow(t.Context(), "SELECT id FROM "+pgx.Identifier{table}.Sanitize()+" WHERE id=$1 FOR UPDATE", id).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	return tx, tx.Conn().PgConn().PID()
}
func nativeFixWait(t *testing.T, pool *pgxpool.Pool, pid uint32, expiry time.Time, queryPart string) {
	t.Helper()
	deadline := time.Now().Add(1800 * time.Millisecond)
	for {
		var blocked bool
		err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE $2)`, int64(pid), "%"+queryPart+"%").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("specific final authorization lock wait not observed")
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	t.Logf("observed actual final %s lock wait behind blocker PID %d", queryPart, pid)
	for {
		var expired bool
		if err := pool.QueryRow(t.Context(), "SELECT clock_timestamp()>$1", expiry).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expiry not reached inside lock_timeout")
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
	t.Log("actual database clock passed authority expiry during retained lock wait")
}
func nativeFixSessionExpiry(t *testing.T, pool *pgxpool.Pool, actor auth.AdminContextClaims) time.Time {
	t.Helper()
	var expiry time.Time
	if err := pool.QueryRow(t.Context(), "UPDATE auth_sessions SET expires_at=clock_timestamp()+interval '650 milliseconds' WHERE id=$1 RETURNING expires_at", actor.SessionID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if !actor.ExpiresAt.After(expiry.Add(time.Minute)) {
		t.Fatal("real signed administrative token must outlive login-session expiry")
	}
	return expiry
}
func nativeFixRefreshSession(t *testing.T, pool *pgxpool.Pool, actor auth.AdminContextClaims) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "UPDATE auth_sessions SET expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1", actor.SessionID); err != nil {
		t.Fatal(err)
	}
}
func TestNativeOnboardingCreateFinalOwnerSessionExpiryDB(t *testing.T) {
	pool, actor, _, _, _ := nativestorage.NativeDomainCrossOwnerFixtureForTest(t, false)
	libs := nativeFixLibraries(pool, nil)
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE kind='platform'").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	before := nativeFixRows(t, pool)
	blocker, pid := nativeFixBlock(t, pool, "resource_owners", owner)
	expiry := nativeFixSessionExpiry(t, pool, actor)
	result := make(chan error, 1)
	go func() {
		_, err := libs.Create(t.Context(), actor, nativestorage.LibraryCreateCommand{Name: "Expired owner wait"})
		result <- err
	}()
	nativeFixWait(t, pool, pid, expiry, "resource_owners")
	if err := blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	nativeFixCode(t, <-result, "authorization_state_stale")
	if before != nativeFixRows(t, pool) {
		t.Error("expired owner-wait Create inserted folder/marker/canonical-group or changed retained rows")
	}
}
func TestNativeOnboardingBindFinalEntitlementSessionExpiryDB(t *testing.T) {
	pool, actor, org, source, id := nativestorage.NativeDomainCrossOwnerFixtureForTest(t, false)
	libs := nativeFixLibraries(pool, nil)
	grant := nativeFixGrant(t, pool, org, source)
	before := nativeFixRows(t, pool)
	blocker, pid := nativeFixBlock(t, pool, "organization_entitlements", grant)
	expiry := nativeFixSessionExpiry(t, pool, actor)
	result := make(chan error, 1)
	go func() {
		_, err := libs.Bind(t.Context(), actor, source.SourceKey, id, source.ConfigurationRevision, 2)
		result <- err
	}()
	nativeFixWait(t, pool, pid, expiry, "organization_entitlements")
	if err := blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	nativeFixCode(t, <-result, "authorization_state_stale")
	if before != nativeFixRows(t, pool) {
		t.Error("expired final entitlement-wait Bind inserted binding/L3 or changed retained rows")
	}
}

// Barrier forwards admission to the actual attached queue. It changes only the
// test's retained authority/lock state after Scan's outer transaction commits.
type nativeFixQueueBarrier struct {
	actual nativestorage.NativeLibraryQueue
	before func() error
}

func (q *nativeFixQueueBarrier) EnqueueNativeLibraryAuthorized(ctx context.Context, id int, authorize func(context.Context, pgx.Tx) error) (*models.ScanRun, bool, error) {
	if err := q.before(); err != nil {
		return nil, false, err
	}
	return q.actual.EnqueueNativeLibraryAuthorized(ctx, id, authorize)
}
func TestNativeOnboardingScanFinalEntitlementExpiryDB(t *testing.T) {
	pool, actor, org, source, id := nativestorage.NativeDomainCrossOwnerFixtureForTest(t, true)
	folders := catalog.NewFolderRepository(pool)
	repo := scanqueue.NewRepository(pool)
	actual := scanqueue.NewService(repo, folders, nil, nil, t.Context(), 1, 1)
	libs := nativeFixLibraries(pool, actual)
	initial, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Start(t.Context(), initial.ScanRunID); err != nil {
		t.Fatal(err)
	}
	grant := nativeFixGrant(t, pool, org, source)
	for _, kind := range []string{"session", "admin_token"} {
		t.Run(kind, func(t *testing.T) {
			nativeFixRefreshSession(t, pool, actor)
			current := actor
			if kind == "admin_token" {
				current.ExpiresAt = time.Now().Truncate(time.Second).Add(2 * time.Second)
				tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
				token, e := tokens.Mint(current)
				if e != nil {
					t.Fatal(e)
				}
				current, e = tokens.Parse(token)
				if e != nil {
					t.Fatal(e)
				}
			}
			before := nativeFixRows(t, pool)
			var blocker pgx.Tx
			var pid uint32
			var expiry time.Time
			started := make(chan struct{})
			result := make(chan error, 1)
			barrier := &nativeFixQueueBarrier{actual: actual, before: func() error {
				// This callback runs on the command goroutine. SQL errors are transported
				// to its result rather than invoking testing.Fatal on that goroutine.
				var e error
				blocker, e = pool.Begin(t.Context())
				if e != nil {
					return e
				}
				var locked uuid.UUID
				e = blocker.QueryRow(t.Context(), "SELECT id FROM organization_entitlements WHERE id=$1 FOR UPDATE", grant).Scan(&locked)
				if e != nil {
					return e
				}
				pid = blocker.Conn().PgConn().PID()
				expiry = current.ExpiresAt
				if kind == "session" {
					e = pool.QueryRow(t.Context(), "UPDATE auth_sessions SET expires_at=clock_timestamp()+interval '650 milliseconds' WHERE id=$1 RETURNING expires_at", current.SessionID).Scan(&expiry)
					if e != nil {
						return e
					}
				}
				close(started)
				return nil
			}}
			blockedLibs := nativeFixLibraries(pool, barrier)
			go func() {
				_, e := blockedLibs.Scan(t.Context(), current, id, 3, source.ConfigurationRevision)
				result <- e
			}()
			select {
			case <-started:
			case e := <-result:
				t.Fatalf("actual queue barrier not reached: %v", e)
			case <-time.After(5 * time.Second):
				t.Fatal("actual queue barrier timeout")
			}
			t.Cleanup(func() { _ = blocker.Rollback(context.Background()) })
			nativeFixWait(t, pool, pid, expiry, "organization_entitlements")
			if e := blocker.Rollback(t.Context()); e != nil {
				t.Fatal(e)
			}
			nativeFixCode(t, <-result, "authorization_state_stale")
			if before != nativeFixRows(t, pool) {
				t.Error("expired actual queue callback inserted work or updated running coalescing/follow-up rows")
			}
		})
	}
}
func TestNativeOnboardingCrossOwnerGrantProjectionDB(t *testing.T) {
	pool, actor, org, source, id := nativestorage.NativeDomainCrossOwnerFixtureForTest(t, true)
	folders := catalog.NewFolderRepository(pool)
	repo := scanqueue.NewRepository(pool)
	queue := scanqueue.NewService(repo, folders, nil, nil, t.Context(), 1, 1)
	libs := nativeFixLibraries(pool, queue)
	grant := nativeFixGrant(t, pool, org, source)
	positive, err := libs.Get(t.Context(), actor, id)
	if err != nil || !positive.ReadyToQueue || !positive.SupportedOperations["full_scan"] || !positive.SupportedOperations["bind"] {
		t.Fatal("legal current grant lacks actual queue readiness", err)
	}
	// Existing sanctioned platform-root trigger created the grant. Revoke that
	// exact current grant with its security revision, then restore by a new grant
	// with the same verified organization/root/installation tuple and provenance.
	var owner uuid.UUID
	var revision int64
	if err = pool.QueryRow(t.Context(), "SELECT root_owner_id,security_revision FROM organization_entitlements WHERE id=$1", grant).Scan(&owner, &revision); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(t.Context(), "UPDATE organization_entitlements SET status='revoked',revoked_at=clock_timestamp(),security_revision=security_revision+1 WHERE id=$1 AND status='active' AND security_revision=$2", grant, revision)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("exact actual grant revoke failed", err)
	}
	before := nativeFixRows(t, pool)
	projected, err := libs.Get(t.Context(), actor, id)
	if err != nil {
		t.Fatal("authorized platform inspection refused", err)
	}
	if projected.ReadyToQueue || projected.SupportedOperations["full_scan"] || projected.SupportedOperations["bind"] || projected.State != "source_unavailable" || projected.SourceAvailability == "attached_enabled" {
		t.Errorf("revoked actual cross-owner grant advertises mutation capability: %+v", projected)
	}
	if projected.SourceKey == nil || *projected.SourceKey != source.SourceKey || projected.BindingID == nil || positive.BindingID == nil || *projected.BindingID != *positive.BindingID || projected.SourceRevision == nil || *projected.SourceRevision != source.ConfigurationRevision || projected.LibraryRevision != 3 || projected.CreationKey != positive.CreationKey {
		t.Error("authorized revoked-grant inspection lost retained S/L or IDs")
	}
	page, err := libs.List(t.Context(), actor, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, status := range page.Libraries {
		if status.LibraryID == id {
			found = true
			if status.ReadyToQueue || status.SupportedOperations["full_scan"] || status.SupportedOperations["bind"] {
				t.Error("List advertises revoked-grant capabilities")
			}
		}
	}
	if !found {
		t.Error("authorized List lost retained library")
	}
	_, err = libs.Bind(t.Context(), actor, source.SourceKey, id, source.ConfigurationRevision, 3)
	nativeFixCode(t, err, "not_found")
	_, err = libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	nativeFixCode(t, err, "not_found")
	if before != nativeFixRows(t, pool) {
		t.Error("revoked grant inspection/commands changed S/L/bindings/queue rows")
	}
	var renewed uuid.UUID
	if err = pool.QueryRow(t.Context(), `INSERT INTO organization_entitlements(organization_id,root_owner_id,root_kind,entitlement_kind,plugin_installation_id,status,granted_by_service)
 VALUES($1,$2,'plugin_installation','plugin_availability',$3,'active','native-authority-fix-test') RETURNING id`, org.OrganizationID, owner, *source.InstallationID).Scan(&renewed); err != nil || renewed == grant {
		t.Fatal("sanctioned current exact grant restore failed", err)
	}
	restored, err := libs.Get(t.Context(), actor, id)
	if err != nil || !restored.ReadyToQueue || !restored.SupportedOperations["full_scan"] || !restored.SupportedOperations["bind"] {
		t.Fatal("restored actual grant did not restore projection", err)
	}
	repeated, err := libs.Bind(t.Context(), actor, source.SourceKey, id, source.ConfigurationRevision, 3)
	if err != nil || !repeated.Repeated || repeated.BindingID != *positive.BindingID {
		t.Fatal("restored exact Bind repeat refused", err)
	}
	scanned, err := libs.Scan(t.Context(), actor, id, 3, source.ConfigurationRevision)
	if err != nil || !scanned.Created || scanned.ScanRunID == "" {
		t.Fatal("restored actual queue admission refused", err)
	}
	t.Log(fmt.Sprintf("grant %s revoked; retained S/L and IDs; actual GET/List/Bind/Scan align; new exact grant %s restores admission", grant, renewed))
}
