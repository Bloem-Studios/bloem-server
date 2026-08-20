package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	updatedEmail := "renamed-" + input.Email
	updated, err := service.Update(ctx, tenant.ID, member.ID, tenancy.UpdateMemberInput{
		Username: &newUsername,
		Email:    &updatedEmail,
	})
	if err != nil || updated.Username != newUsername || updated.Email != updatedEmail {
		t.Fatalf("update = (%+v, %v)", updated, err)
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

	suspended, err := service.Suspend(ctx, tenant.ID, member.ID)
	if err != nil || suspended.Enabled {
		t.Fatalf("suspend = (%+v, %v)", suspended, err)
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
