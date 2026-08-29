package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type observingMemberUserRepository struct {
	base   *auth.UserRepository
	before func()
}

func (r *observingMemberUserRepository) GetByID(ctx context.Context, userID int) (*models.User, error) {
	if r.before != nil {
		r.before()
	}
	return r.base.GetByID(ctx, userID)
}

func newMemberService(t *testing.T) (context.Context, *pgxpool.Pool, *tenancy.Store, *tenancy.MemberService, *auth.SessionRepository) {
	t.Helper()
	ctx, pool, store := testTenantPool(t)
	users := auth.NewUserRepository(pool)
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	sessions := auth.NewSessionRepository(pool)
	return ctx, pool, store, tenancy.NewMemberService(pool, accounts, users, sessions), sessions
}

func createMemberTenant(t *testing.T, ctx context.Context, store *tenancy.Store, slots int) tenancy.TenantOrganization {
	t.Helper()
	tenant, err := store.CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
		Name:               "Member service " + t.Name(),
		ExternalOperatorID: "op-member-tests",
		ExternalServiceID:  fmt.Sprintf("member-%s-%d", t.Name(), time.Now().UnixNano()),
		Slots:              slots,
		Transcodes:         1,
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteTenantOrganization(context.Background(), tenant.ID) })
	return tenant
}

func memberInput(name string) tenancy.CreateMemberInput {
	return tenancy.CreateMemberInput{
		Username: name,
		Email:    name + "@members.test",
		Password: "member-password-" + name,
	}
}

func TestMemberServiceCreateEnforcesSlotQuotaAtomically(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)

	type result struct {
		userID int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			ready.Done()
			<-start
			user, replay, err := service.Create(ctx, tenant.ID, fmt.Sprintf("quota-command-%d", i), memberInput(fmt.Sprintf("quota-member-%d-%d", time.Now().UnixNano(), i)))
			if replay {
				err = fmt.Errorf("first command reported replay")
			}
			results <- result{userID: user.ID, err: err}
		}()
	}
	ready.Wait()
	close(start)

	var successes, quotaFailures int
	var createdUserID int
	for range 2 {
		got := <-results
		switch {
		case got.err == nil:
			successes++
			createdUserID = got.userID
		case errors.Is(got.err, tenancy.ErrSlotQuotaExceeded):
			quotaFailures++
		default:
			t.Fatalf("create result error = %v", got.err)
		}
	}
	if successes != 1 || quotaFailures != 1 || createdUserID == 0 {
		t.Fatalf("race results: successes=%d quota_failures=%d user_id=%d", successes, quotaFailures, createdUserID)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, createdUserID) })
	members, err := service.List(ctx, tenant.ID)
	if err != nil || len(members) != 1 || members[0].ID != createdUserID {
		t.Fatalf("members = %+v, %v", members, err)
	}
}

func TestMemberServiceCreateReplaysSameCommandAndRejectsChangedCommand(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("replay-member-%d", time.Now().UnixNano()))

	first, replay, err := service.Create(ctx, tenant.ID, "stable-command", input)
	if err != nil || replay {
		t.Fatalf("first create = (%+v, replay=%v, %v)", first, replay, err)
	}
	var tenantGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM access_groups
		WHERE organization_id = $1 AND is_default`, tenant.ID).Scan(&tenantGroupID); err != nil {
		t.Fatalf("load tenant default group: %v", err)
	}
	if first.AccessGroupID == nil || *first.AccessGroupID != tenantGroupID {
		t.Fatalf("member access group = %v, want tenant default group %d", first.AccessGroupID, tenantGroupID)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, first.ID) })
	second, replay, err := service.Create(ctx, tenant.ID, "stable-command", input)
	if err != nil || !replay || second.ID != first.ID {
		t.Fatalf("replay = (%+v, replay=%v, %v), want user %d", second, replay, err, first.ID)
	}

	changed := input
	changed.Password = "different-secret"
	if _, _, err := service.Create(ctx, tenant.ID, "stable-command", changed); !errors.Is(err, tenancy.ErrIdempotencyConflict) {
		t.Fatalf("changed command error = %v, want ErrIdempotencyConflict", err)
	}
	members, err := service.List(ctx, tenant.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members after replay = %d, %v", len(members), err)
	}
}

func TestMemberServiceConcurrentSameCommandCreatesOneDurableReceipt(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("same-command-%d", time.Now().UnixNano()))
	type result struct {
		user   models.User
		replay bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			user, replay, err := service.Create(ctx, tenant.ID, "same-race-command", input)
			results <- result{user: user, replay: replay, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.user.ID == 0 || first.user.ID != second.user.ID || first.replay == second.replay {
		t.Fatalf("same command race = first(%d,%v,%v) second(%d,%v,%v)", first.user.ID, first.replay, first.err, second.user.ID, second.replay, second.err)
	}
	var memberships, receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1`, tenant.ID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tenant_member_command_receipts WHERE organization_id=$1`, tenant.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 || receipts != 1 {
		t.Fatalf("same command race rows: memberships=%d receipts=%d", memberships, receipts)
	}
}

func TestMemberServiceIdempotencyReceiptSurvivesMutationAndDeletion(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("durable-replay-%d", time.Now().UnixNano()))
	created, replay, err := service.Create(ctx, tenant.ID, "durable-command", input)
	if err != nil || replay {
		t.Fatalf("create = (%+v, replay=%v, %v)", created, replay, err)
	}

	renamed := input.Username + "-renamed"
	if _, err := service.Update(ctx, tenant.ID, created.ID, tenancy.UpdateMemberInput{Username: &renamed}); err != nil {
		t.Fatalf("rename member: %v", err)
	}
	replayed, replay, err := service.Create(ctx, tenant.ID, "durable-command", input)
	if err != nil || !replay || replayed.ID != created.ID || replayed.Username != input.Username {
		t.Fatalf("replay after mutation = (%+v, replay=%v, %v), want immutable result for %d", replayed, replay, err, created.ID)
	}
	if err := service.Delete(ctx, tenant.ID, created.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if err := service.Delete(ctx, tenant.ID, created.ID); err != nil {
		t.Fatalf("repeat delete must be idempotent: %v", err)
	}
	replayed, replay, err = service.Create(ctx, tenant.ID, "durable-command", input)
	if err != nil || !replay || replayed.ID != created.ID || replayed.Username != input.Username {
		t.Fatalf("replay after deletion = (%+v, replay=%v, %v), want tombstoned result for %d", replayed, replay, err, created.ID)
	}
}

func TestMemberServiceDeleteRetryIsDurableForPreReceiptMember(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	username := fmt.Sprintf("pre-receipt-%d", time.Now().UnixNano())
	var userID int
	if err := pool.QueryRow(ctx, `INSERT INTO users
		(username,email,password_hash,role,enabled)
		VALUES ($1,$2,'legacy-hash','user',true) RETURNING id`, username, username+"@members.test").Scan(&userID); err != nil {
		t.Fatalf("seed pre-receipt user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships
		(organization_id,account_id,status,legacy_role)
		VALUES ($1,$2,'active','user')`, tenant.ID, userID); err != nil {
		t.Fatalf("seed pre-receipt membership: %v", err)
	}

	if err := service.Delete(ctx, tenant.ID, userID); err != nil {
		t.Fatalf("delete pre-receipt member: %v", err)
	}
	if err := service.Delete(ctx, tenant.ID, userID); err != nil {
		t.Fatalf("retry pre-receipt member delete: %v", err)
	}
	var tombstoned bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_member_command_receipts
		WHERE organization_id=$1 AND result_account_id=$2 AND deleted_at IS NOT NULL)`, tenant.ID, userID).Scan(&tombstoned); err != nil || !tombstoned {
		t.Fatalf("pre-receipt delete tombstone = %v, %v", tombstoned, err)
	}
}

func TestMemberServiceDeletePreservesGlobalUserWithAnotherMembership(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenantA := createMemberTenant(t, ctx, store, 2)
	tenantB := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("shared-member-%d", time.Now().UnixNano()))
	member, _, err := service.Create(ctx, tenantA.ID, "shared-command", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')`, tenantB.ID, member.ID); err != nil {
		t.Fatalf("seed second membership: %v", err)
	}
	var groupB int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, tenantB.ID).Scan(&groupB); err != nil {
		t.Fatal(err)
	}
	profileB := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,'Tenant B',$3,$4)`, profileB, member.ID, tenantB.ID, groupB); err != nil {
		t.Fatalf("seed tenant B profile: %v", err)
	}

	if err := service.Delete(ctx, tenantA.ID, member.ID); err != nil {
		t.Fatalf("delete tenant A membership: %v", err)
	}
	var userPresent, membershipBPresent, profileBPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, member.ID).Scan(&userPresent); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2)`, tenantB.ID, member.ID).Scan(&membershipBPresent); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_profiles WHERE organization_id=$1 AND user_id=$2 AND id=$3)`, tenantB.ID, member.ID, profileB).Scan(&profileBPresent); err != nil {
		t.Fatal(err)
	}
	if !userPresent || !membershipBPresent || !profileBPresent {
		t.Fatalf("cross-tenant data was deleted: user=%v membership_b=%v profile_b=%v", userPresent, membershipBPresent, profileBPresent)
	}
}

func TestMemberServiceDeleteOwnerPreservesAdminFreezeAndChoosesActiveReplacement(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 3)
	owner, _, err := service.Create(ctx, tenant.ID, "owner-command", memberInput(fmt.Sprintf("owner-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	suspended, _, err := service.Create(ctx, tenant.ID, "suspended-command", memberInput(fmt.Sprintf("suspended-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	active, _, err := service.Create(ctx, tenant.ID, "active-command", memberInput(fmt.Sprintf("active-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Suspend(ctx, tenant.ID, suspended.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetTenantOrganizationFrozen(ctx, tenant.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, tenant.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	var replacement *int
	var status, reason string
	if err := pool.QueryRow(ctx, `SELECT owner_account_id,status,suspension_reason FROM organizations WHERE id=$1`, tenant.ID).Scan(&replacement, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if replacement == nil || *replacement != active.ID || status != "suspended" || reason != "admin" {
		t.Fatalf("tenant after owner delete = owner=%v status=%q reason=%q, want active owner %d with admin freeze", replacement, status, reason, active.ID)
	}
}

func TestMemberServiceDeletePreservesPlatformAdminObligations(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	member, _, err := service.Create(ctx, tenant.ID, "admin-obligation-command", memberInput(fmt.Sprintf("admin-obligation-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE platform_security SET owner_account_id=$1,ownership_resolution_required=false`, member.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE platform_security SET owner_account_id=NULL,ownership_resolution_required=true WHERE owner_account_id=$1`, member.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, member.ID)
	})
	if err := service.Delete(ctx, tenant.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	var userPresent, membershipPresent bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, member.ID).Scan(&userPresent); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2)`, tenant.ID, member.ID).Scan(&membershipPresent); err != nil {
		t.Fatal(err)
	}
	if !userPresent || membershipPresent {
		t.Fatalf("platform admin delete state: user=%v membership=%v", userPresent, membershipPresent)
	}
}

func TestMemberServiceDeleteRollsBackOwnershipWhenMembershipRemovalFails(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	owner, _, err := service.Create(ctx, tenant.ID, "rollback-owner", memberInput(fmt.Sprintf("rollback-owner-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	replacement, _, err := service.Create(ctx, tenant.ID, "rollback-replacement", memberInput(fmt.Sprintf("rollback-replacement-%d", time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	triggerName := fmt.Sprintf("fail_member_delete_%d", time.Now().UnixNano())
	functionName := triggerName + "_fn"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected membership delete failure'; END $$;
		CREATE TRIGGER %s BEFORE DELETE ON organization_memberships FOR EACH ROW WHEN (OLD.account_id = %d) EXECUTE FUNCTION %s()`, functionName, triggerName, owner.ID, functionName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON organization_memberships", triggerName))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})
	if err := service.Delete(ctx, tenant.ID, owner.ID); err == nil {
		t.Fatal("delete unexpectedly succeeded through injected membership failure")
	}
	var ownerAfter *int
	var membershipPresent, userPresent bool
	if err := pool.QueryRow(ctx, `SELECT owner_account_id FROM organizations WHERE id=$1`, tenant.ID).Scan(&ownerAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$1 AND account_id=$2)`, tenant.ID, owner.ID).Scan(&membershipPresent); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, owner.ID).Scan(&userPresent); err != nil {
		t.Fatal(err)
	}
	if ownerAfter == nil || *ownerAfter != owner.ID || !membershipPresent || !userPresent {
		t.Fatalf("failed delete committed partial state: owner=%v membership=%v user=%v replacement=%d", ownerAfter, membershipPresent, userPresent, replacement.ID)
	}
}

func TestMemberServiceIdentityAndSessionRevocationAreAtomic(t *testing.T) {
	ctx, pool, store, service, sessions := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("atomic-member-%d", time.Now().UnixNano()))
	member, _, err := service.Create(ctx, tenant.ID, "atomic-command", input)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: sessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	triggerName := fmt.Sprintf("fail_revoke_%d", time.Now().UnixNano())
	functionName := triggerName + "_fn"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected revoke failure'; END $$;
		CREATE TRIGGER %s BEFORE UPDATE ON auth_sessions FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON auth_sessions", triggerName))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
	})
	updatedEmail := "changed-" + input.Email
	if _, err := service.Update(ctx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Email: &updatedEmail}); err == nil {
		t.Fatal("email update unexpectedly succeeded despite revoke failure")
	}
	stored, err := service.Get(ctx, tenant.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Email != input.Email {
		t.Fatalf("email committed despite revoke failure: got %q want %q", stored.Email, input.Email)
	}
	if _, err := service.ResetPassword(ctx, tenant.ID, member.ID, "replacement-password"); err == nil {
		t.Fatal("password reset unexpectedly succeeded despite revoke failure")
	}
	stored, err = service.Get(ctx, tenant.ID, member.ID)
	if err != nil || !auth.CheckPassword(&stored, input.Password) {
		t.Fatalf("password committed despite revoke failure: user=%+v err=%v", stored, err)
	}
	if _, err := service.Suspend(ctx, tenant.ID, member.ID); err == nil {
		t.Fatal("suspend unexpectedly succeeded despite revoke failure")
	}
	stored, err = service.Get(ctx, tenant.ID, member.ID)
	if err != nil || !stored.Enabled {
		t.Fatalf("account disable committed despite revoke failure: user=%+v err=%v", stored, err)
	}
	var membershipStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, tenant.ID, member.ID).Scan(&membershipStatus); err != nil {
		t.Fatal(err)
	}
	if membershipStatus != "active" {
		t.Fatalf("membership status committed despite revoke failure: %q", membershipStatus)
	}
}

func TestMemberServiceCallerOwnedTransactionControlsIdentityMutation(t *testing.T) {
	ctx, pool, store, service, sessions := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	member, _, err := service.Create(ctx, tenant.ID, "caller-tx-create-"+uuid.NewString(), memberInput("caller-tx-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, member.ID) })
	sessionID := uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: sessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("create member session: %v", err)
	}

	rolledBackName := "rolled-back-" + uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if _, err := service.UpdateInTransaction(ctx, tx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &rolledBackName}); err != nil {
		t.Fatalf("update in rollback transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	unchanged, err := service.Get(ctx, tenant.ID, member.ID)
	if err != nil || unchanged.Username != member.Username {
		t.Fatalf("after rollback = %+v, %v; want username %q", unchanged, err, member.Username)
	}
	if valid, err := sessions.IsValid(ctx, sessionID); err != nil || !valid {
		t.Fatalf("session after rollback valid = %v, error = %v", valid, err)
	}

	committedName := "committed-" + uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin commit transaction: %v", err)
	}
	updated, err := service.UpdateInTransaction(ctx, tx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &committedName})
	if err != nil {
		t.Fatalf("update in commit transaction: %v", err)
	}
	if updated.Username != committedName {
		t.Fatalf("transaction response username = %q, want %q", updated.Username, committedName)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := service.Get(ctx, tenant.ID, member.ID)
	if err != nil || got.Username != committedName {
		t.Fatalf("after commit = %+v, %v; want username %q", got, err, committedName)
	}
	if valid, err := sessions.IsValid(ctx, sessionID); err != nil || valid {
		t.Fatalf("session after commit valid = %v, error = %v", valid, err)
	}
}

func TestMemberServiceCallerOwnedTransactionControlsDelete(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	member, _, err := service.Create(ctx, tenant.ID, "caller-delete-create-"+uuid.NewString(), memberInput("caller-delete-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if err := service.DeleteInTransaction(ctx, tx, tenant.ID, member.ID); err != nil {
		t.Fatalf("delete in rollback transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := service.Get(ctx, tenant.ID, member.ID); err != nil {
		t.Fatalf("member missing after rollback: %v", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin commit transaction: %v", err)
	}
	if err := service.DeleteInTransaction(ctx, tx, tenant.ID, member.ID); err != nil {
		t.Fatalf("delete in commit transaction: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := service.CompleteDeleteAfterCommit(ctx, tenant.ID, member.ID); err != nil {
		t.Fatalf("complete delete: %v", err)
	}
	if _, err := service.Get(ctx, tenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("get after commit error = %v, want member not found", err)
	}
}

func TestMemberServiceCompatInvalidationRunsAfterCommittedSecurityMutation(t *testing.T) {
	ctx, pool, store, service, sessions := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("compat-lifecycle-%d", time.Now().UnixNano()))
	member, _, err := service.Create(ctx, tenant.ID, "compat-lifecycle-command", input)
	if err != nil {
		t.Fatal(err)
	}
	compatErr := errors.New("compat eviction failed")
	assertCommittedCallback := func(check func(context.Context) bool) func(context.Context, int) error {
		return func(callbackCtx context.Context, userID int) error {
			if userID != member.ID {
				t.Errorf("compat invalidation user = %d, want %d", userID, member.ID)
			}
			queryCtx, cancel := context.WithTimeout(callbackCtx, time.Second)
			defer cancel()
			if !check(queryCtx) {
				t.Error("compat invalidation ran before the durable mutation was visible")
			}
			return compatErr
		}
	}

	newUsername := input.Username + "-renamed"
	newEmail := "renamed-" + input.Email
	updateSessionID := uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{ID: updateSessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	service.SetCompatSessionInvalidator(assertCommittedCallback(func(queryCtx context.Context) bool {
		var username, email string
		var revoked bool
		err := pool.QueryRow(queryCtx, `SELECT username,email FROM users WHERE id=$1`, member.ID).Scan(&username, &email)
		if err != nil || username != newUsername || email != newEmail {
			return false
		}
		err = pool.QueryRow(queryCtx, `SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1`, updateSessionID).Scan(&revoked)
		return err == nil && revoked
	}))
	if _, err := service.Update(ctx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &newUsername, Email: &newEmail}); !errors.Is(err, compatErr) {
		t.Fatalf("update error = %v, want committed compat failure", err)
	}
	stored, err := service.Get(ctx, tenant.ID, member.ID)
	if err != nil || stored.Username != newUsername || stored.Email != newEmail {
		t.Fatalf("identity did not remain committed after compat failure: user=%+v err=%v", stored, err)
	}

	resetPassword := "replacement-compat-password"
	service.SetCompatSessionInvalidator(assertCommittedCallback(func(queryCtx context.Context) bool {
		var passwordHash string
		if err := pool.QueryRow(queryCtx, `SELECT password_hash FROM users WHERE id=$1`, member.ID).Scan(&passwordHash); err != nil {
			return false
		}
		return auth.CheckPassword(&models.User{PasswordHash: passwordHash}, resetPassword)
	}))
	if _, err := service.ResetPassword(ctx, tenant.ID, member.ID, resetPassword); !errors.Is(err, compatErr) {
		t.Fatalf("reset error = %v, want committed compat failure", err)
	}
	stored, err = service.Get(ctx, tenant.ID, member.ID)
	if err != nil || !auth.CheckPassword(&stored, resetPassword) {
		t.Fatalf("password did not remain committed after compat failure: user=%+v err=%v", stored, err)
	}

	service.SetCompatSessionInvalidator(assertCommittedCallback(func(queryCtx context.Context) bool {
		var enabled bool
		var status string
		err := pool.QueryRow(queryCtx, `SELECT u.enabled,m.status FROM users u
			JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=$1
			WHERE u.id=$2`, tenant.ID, member.ID).Scan(&enabled, &status)
		return err == nil && !enabled && status == "suspended"
	}))
	if _, err := service.Suspend(ctx, tenant.ID, member.ID); !errors.Is(err, compatErr) {
		t.Fatalf("suspend error = %v, want committed compat failure", err)
	}
	stored, err = service.Get(ctx, tenant.ID, member.ID)
	if err != nil || stored.Enabled {
		t.Fatalf("suspend did not remain committed after compat failure: user=%+v err=%v", stored, err)
	}
}

func TestMemberServiceCompatInvalidationSurvivesRequestCancellationAndPrecedesReload(t *testing.T) {
	for _, operation := range []string{"identity update", "password reset", "suspend"} {
		t.Run(operation, func(t *testing.T) {
			ctx, pool, store := testTenantPool(t)
			users := auth.NewUserRepository(pool)
			accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
			observingUsers := &observingMemberUserRepository{base: users}
			service := tenancy.NewMemberService(pool, accounts, observingUsers, auth.NewSessionRepository(pool))
			tenant := createMemberTenant(t, ctx, store, 1)
			input := memberInput(fmt.Sprintf("cancel-member-%s-%d", strings.ReplaceAll(operation, " ", "-"), time.Now().UnixNano()))
			member, _, err := service.Create(ctx, tenant.ID, "cancel-command-"+operation, input)
			if err != nil {
				t.Fatal(err)
			}

			requestCtx, cancelRequest := context.WithCancel(ctx)
			durableCompatSession := true
			memoryCompatSession := true
			reloadObservedPurged := false
			observingUsers.before = func() {
				reloadObservedPurged = !durableCompatSession && !memoryCompatSession
			}
			service.SetCompatSessionInvalidator(func(callbackCtx context.Context, userID int) error {
				if userID != member.ID {
					t.Errorf("compat invalidation user = %d, want %d", userID, member.ID)
				}
				cancelRequest()
				if err := callbackCtx.Err(); err != nil {
					return err
				}
				durableCompatSession = false
				memoryCompatSession = false
				return nil
			})

			switch operation {
			case "identity update":
				username := input.Username + "-changed"
				_, _ = service.Update(requestCtx, tenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &username})
			case "password reset":
				_, _ = service.ResetPassword(requestCtx, tenant.ID, member.ID, "replacement-password")
			case "suspend":
				_, _ = service.Suspend(requestCtx, tenant.ID, member.ID)
			}
			if durableCompatSession || memoryCompatSession {
				t.Fatalf("compat sessions remain: durable=%v memory=%v", durableCompatSession, memoryCompatSession)
			}
			if !reloadObservedPurged {
				t.Fatal("response reload ran before mandatory compat invalidation")
			}
			if !errors.Is(requestCtx.Err(), context.Canceled) {
				t.Fatalf("request context error = %v, want canceled by post-commit callback", requestCtx.Err())
			}
			switch operation {
			case "identity update":
				var username string
				if err := pool.QueryRow(context.Background(), `SELECT username FROM users WHERE id=$1`, member.ID).Scan(&username); err != nil || username != input.Username+"-changed" {
					t.Fatalf("committed username = %q, %v", username, err)
				}
			case "password reset":
				var passwordHash string
				if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id=$1`, member.ID).Scan(&passwordHash); err != nil || !auth.CheckPassword(&models.User{PasswordHash: passwordHash}, "replacement-password") {
					t.Fatalf("committed password reset missing: %v", err)
				}
			case "suspend":
				var enabled bool
				var status string
				if err := pool.QueryRow(context.Background(), `SELECT u.enabled,m.status FROM users u JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=$1 WHERE u.id=$2`, tenant.ID, member.ID).Scan(&enabled, &status); err != nil || enabled || status != "suspended" {
					t.Fatalf("committed suspension = enabled %v status %q, %v", enabled, status, err)
				}
			}
		})
	}
}

func TestMemberServiceCreateLazilyEnsuresLegacyTenantDefaultGroup(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	if _, err := pool.Exec(ctx, `DELETE FROM access_groups WHERE organization_id=$1`, tenant.ID); err != nil {
		t.Fatalf("remove default group to model legacy tenant: %v", err)
	}
	member, replay, err := service.Create(ctx, tenant.ID, "legacy-group-command", memberInput(fmt.Sprintf("legacy-group-%d", time.Now().UnixNano())))
	if err != nil || replay || member.AccessGroupID == nil {
		t.Fatalf("create with legacy tenant = (%+v, replay=%v, %v)", member, replay, err)
	}
}

func TestMemberServiceCreateLazilyPromotesExistingNamedDefaultGroup(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	var existingGroupID int64
	if err := pool.QueryRow(ctx, `UPDATE access_groups SET is_default=false
		WHERE organization_id=$1 RETURNING id`, tenant.ID).Scan(&existingGroupID); err != nil {
		t.Fatalf("make named default group legacy non-default: %v", err)
	}
	member, replay, err := service.Create(ctx, tenant.ID, "named-group-command", memberInput(fmt.Sprintf("named-group-%d", time.Now().UnixNano())))
	if err != nil || replay {
		t.Fatalf("create with existing named group = (%+v, replay=%v, %v)", member, replay, err)
	}
	if member.AccessGroupID == nil || *member.AccessGroupID != existingGroupID {
		t.Fatalf("member group = %v, want promoted existing group %d", member.AccessGroupID, existingGroupID)
	}
}

func TestMemberServiceCreateLazilyPromotesLowestExistingGroup(t *testing.T) {
	ctx, pool, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 1)
	var firstGroupID int64
	if err := pool.QueryRow(ctx, `UPDATE access_groups SET name='First policy',is_default=false
		WHERE organization_id=$1 RETURNING id`, tenant.ID).Scan(&firstGroupID); err != nil {
		t.Fatalf("make first group non-default: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id,name,is_default)
		VALUES ($1,'Second policy',false)`, tenant.ID); err != nil {
		t.Fatalf("seed second group: %v", err)
	}
	member, replay, err := service.Create(ctx, tenant.ID, "multiple-group-command", memberInput(fmt.Sprintf("multiple-group-%d", time.Now().UnixNano())))
	if err != nil || replay {
		t.Fatalf("create with existing policy groups = (%+v, replay=%v, %v)", member, replay, err)
	}
	if member.AccessGroupID == nil || *member.AccessGroupID != firstGroupID {
		t.Fatalf("member group = %v, want lowest existing group %d", member.AccessGroupID, firstGroupID)
	}
	var groups int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_groups WHERE organization_id=$1`, tenant.ID).Scan(&groups); err != nil || groups != 2 {
		t.Fatalf("group count = %d, %v; want existing two groups only", groups, err)
	}
}

func TestMemberServiceValidatesCanonicalCreateCommand(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	cases := []struct {
		name string
		key  string
		in   tenancy.CreateMemberInput
	}{
		{name: "malformed email", key: "bad-email", in: tenancy.CreateMemberInput{Username: "valid-user", Email: "not-an-email", Password: "valid-password"}},
		{name: "short password", key: "short-password", in: tenancy.CreateMemberInput{Username: "valid-user", Email: "valid@example.test", Password: "short"}},
		{name: "bcrypt overflow", key: "long-password", in: tenancy.CreateMemberInput{Username: "valid-user", Email: "valid@example.test", Password: strings.Repeat("x", 73)}},
		{name: "overlong key", key: strings.Repeat("k", 256), in: tenancy.CreateMemberInput{Username: "valid-user", Email: "valid@example.test", Password: "valid-password"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := service.Create(ctx, tenant.ID, tc.key, tc.in); !errors.Is(err, tenancy.ErrInvalidMemberCommand) {
				t.Fatalf("Create error = %v, want ErrInvalidMemberCommand", err)
			}
		})
	}
}

func TestMemberServiceCreateRejectsFrozenAndRetiredTenant(t *testing.T) {
	ctx, _, store, service, _ := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	if _, err := store.SetTenantOrganizationFrozen(ctx, tenant.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, tenant.ID, "frozen-command", memberInput(fmt.Sprintf("frozen-%d", time.Now().UnixNano()))); !errors.Is(err, tenancy.ErrTenantFrozen) {
		t.Fatalf("frozen create error = %v, want ErrTenantFrozen", err)
	}
	if err := store.DeleteTenantOrganization(ctx, tenant.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, tenant.ID, "retired-command", memberInput(fmt.Sprintf("retired-%d", time.Now().UnixNano()))); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("retired create error = %v, want ErrMemberNotFound", err)
	}
}

func TestMemberServiceLifecycleIsTenantScopedAndRevokesResetSessions(t *testing.T) {
	ctx, _, store, service, sessions := newMemberService(t)
	tenant := createMemberTenant(t, ctx, store, 2)
	otherTenant := createMemberTenant(t, ctx, store, 2)
	input := memberInput(fmt.Sprintf("lifecycle-%d", time.Now().UnixNano()))
	member, _, err := service.Create(ctx, tenant.ID, "lifecycle-command", input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Delete(context.Background(), tenant.ID, member.ID) })

	if _, err := service.Get(ctx, otherTenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant get = %v", err)
	}
	newUsername := input.Username + "-renamed"
	if _, err := service.Update(ctx, otherTenant.ID, member.ID, tenancy.UpdateMemberInput{Username: &newUsername}); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant update = %v", err)
	}
	if _, err := service.Suspend(ctx, otherTenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant suspend = %v", err)
	}
	if _, err := service.Resume(ctx, otherTenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant resume = %v", err)
	}
	if _, err := service.ResetPassword(ctx, otherTenant.ID, member.ID, "other-password"); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant reset = %v", err)
	}
	if err := service.Delete(ctx, otherTenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("cross-tenant delete = %v", err)
	}

	updateSessionID := uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{
		ID: updateSessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create pre-update session: %v", err)
	}
	updatedEmail := "renamed-" + input.Email
	updated, err := service.Update(ctx, tenant.ID, member.ID, tenancy.UpdateMemberInput{
		Username: &newUsername,
		Email:    &updatedEmail,
	})
	if err != nil || updated.Username != newUsername || updated.Email != updatedEmail {
		t.Fatalf("update = (%+v, %v)", updated, err)
	}
	updatedSession, err := sessions.GetByID(ctx, updateSessionID)
	if err != nil || updatedSession.RevokedAt == nil {
		t.Fatalf("session after identity update = (%+v, %v)", updatedSession, err)
	}

	sessionID := fmt.Sprintf("11f41546-c67e-4c14-8c3d-%012d", member.ID)
	if err := sessions.Create(ctx, models.AuthSession{
		ID: sessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	reset, err := service.ResetPassword(ctx, tenant.ID, member.ID, "replacement-secret")
	if err != nil || !auth.CheckPassword(&reset, "replacement-secret") {
		t.Fatalf("reset = (%+v, %v)", reset, err)
	}
	storedSession, err := sessions.GetByID(ctx, sessionID)
	if err != nil || storedSession.RevokedAt == nil {
		t.Fatalf("session after reset = (%+v, %v)", storedSession, err)
	}

	suspendSessionID := uuid.NewString()
	if err := sessions.Create(ctx, models.AuthSession{
		ID: suspendSessionID, UserID: member.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create pre-suspend session: %v", err)
	}
	suspended, err := service.Suspend(ctx, tenant.ID, member.ID)
	if err != nil || suspended.Enabled {
		t.Fatalf("suspend = (%+v, %v)", suspended, err)
	}
	suspendedSession, err := sessions.GetByID(ctx, suspendSessionID)
	if err != nil || suspendedSession.RevokedAt == nil {
		t.Fatalf("session after suspend = (%+v, %v)", suspendedSession, err)
	}
	resumed, err := service.Resume(ctx, tenant.ID, member.ID)
	if err != nil || !resumed.Enabled {
		t.Fatalf("resume = (%+v, %v)", resumed, err)
	}

	if err := service.Delete(ctx, tenant.ID, member.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := service.Get(ctx, tenant.ID, member.ID); !errors.Is(err, tenancy.ErrMemberNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
}
