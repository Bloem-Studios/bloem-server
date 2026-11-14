package adminpeople

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// TestJudgeMatrix pins the authority table in the Judge comment: one row per
// target class, one column per actor.
func TestJudgeMatrix(t *testing.T) {
	const actorID = 10
	target := func(id int, mutate func(*TargetFacts)) TargetFacts {
		facts := TargetFacts{AccountID: id, UserRole: "user", LegacyRole: "user"}
		if mutate != nil {
			mutate(&facts)
		}
		return facts
	}
	orgAdmin := ActorFacts{AccountID: actorID, Authority: AuthorityOrganizationAdmin}
	orgOwner := ActorFacts{AccountID: actorID, Authority: AuthorityOrganizationAdmin, IsOrganizationOwner: true}
	platform := ActorFacts{AccountID: actorID, Authority: AuthorityPlatformAdmin}
	serverOwner := ActorFacts{AccountID: actorID, Authority: AuthorityPlatformAdmin, IsServerOwner: true}

	cases := []struct {
		name   string
		actor  ActorFacts
		target TargetFacts
		class  TargetClass
		allow  bool
	}{
		{"oa member", orgAdmin, target(11, nil), TargetMember, true},
		{"oa administrator", orgAdmin, target(11, func(f *TargetFacts) { f.LegacyRole = "admin" }), TargetOrganizationAdmin, false},
		{"oa owner", orgAdmin, target(11, func(f *TargetFacts) { f.IsOrganizationOwner = true; f.LegacyRole = "admin" }), TargetOrganizationOwner, false},
		{"oa platform account", orgAdmin, target(11, func(f *TargetFacts) { f.UserRole = "admin" }), TargetPlatformAccount, false},
		{"oa self", orgAdmin, target(actorID, nil), TargetSelf, false},

		{"oo member", orgOwner, target(11, nil), TargetMember, true},
		{"oo administrator", orgOwner, target(11, func(f *TargetFacts) { f.LegacyRole = "admin" }), TargetOrganizationAdmin, true},
		{"oo platform account", orgOwner, target(11, func(f *TargetFacts) { f.UserRole = "admin" }), TargetPlatformAccount, false},
		{"oo self", orgOwner, target(actorID, func(f *TargetFacts) { f.IsOrganizationOwner = true }), TargetSelf, false},

		{"pa member", platform, target(11, nil), TargetMember, true},
		{"pa administrator", platform, target(11, func(f *TargetFacts) { f.LegacyRole = "admin" }), TargetOrganizationAdmin, true},
		{"pa owner", platform, target(11, func(f *TargetFacts) { f.IsOrganizationOwner = true; f.LegacyRole = "admin" }), TargetOrganizationOwner, true},
		{"pa other platform account", platform, target(11, func(f *TargetFacts) { f.UserRole = "admin" }), TargetPlatformAccount, false},
		{"pa server owner account", platform, target(11, func(f *TargetFacts) { f.UserRole = "admin"; f.IsServerOwner = true }), TargetPlatformAccount, false},
		{"pa self", platform, target(actorID, func(f *TargetFacts) { f.UserRole = "admin" }), TargetSelf, false},
		{"server owner other platform account", serverOwner, target(11, func(f *TargetFacts) { f.UserRole = "admin" }), TargetPlatformAccount, true},
		{"server owner self", serverOwner, target(actorID, func(f *TargetFacts) { f.UserRole = "admin"; f.IsServerOwner = true }), TargetSelf, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, err := Judge(tc.actor, tc.target)
			if class != tc.class {
				t.Fatalf("class = %s, want %s", class, tc.class)
			}
			if tc.allow {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var refusal *TargetProtectedError
			if !errors.Is(err, ErrTargetProtected) || !errors.As(err, &refusal) || refusal.Reason != tc.class {
				t.Fatalf("error = %v, want target protected (%s)", err, tc.class)
			}
		})
	}
}

// authorityFixture is organization A of the people fixture with one of each
// kind of person: its owner, a second administrator, an ordinary member and a
// platform account that is a member.
type authorityFixture struct {
	*peopleFixture
	adminID, memberID, platformID int
	adminProfile, memberProfile   string
}

func newAuthorityFixture(t *testing.T) *authorityFixture {
	t.Helper()
	f := &authorityFixture{peopleFixture: newPeopleFixture(t)}
	f.adminID, f.adminProfile = f.addAccount(t, f.orgA, "second-admin@example.test", "Second Admin", "Admin Profile", f.groupA, true)
	f.memberID, f.memberProfile = f.addAccount(t, f.orgA, "member@example.test", "Member", "Member Profile", f.groupA, false)
	f.platformID = f.insertUser(t, "platform@example.test", "Platform Account")
	f.addMembership(t, f.orgA, f.platformID, "user")
	if _, err := f.pool.Exec(f.ctx, `UPDATE users SET role='admin' WHERE id=$1`, f.platformID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *authorityFixture) revision(t *testing.T, accountID int) int64 {
	t.Helper()
	var revision int64
	if err := f.pool.QueryRow(f.ctx, `SELECT security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, f.orgA, accountID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func (f *authorityFixture) auditCount(t *testing.T, action, outcome string) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM admin_audit_events WHERE organization_id=$1 AND action=$2 AND outcome=$3`, f.orgA, action, outcome).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestUpdateMembershipFollowsAuthorityRules(t *testing.T) {
	f := newAuthorityFixture(t)
	suspend := func(actor, target int) error {
		_, err := f.service.UpdateMembership(f.ctx, f.orgA, actor, target, f.revision(t, target), tenancy.MembershipSuspended)
		return err
	}
	assertProtected := func(err error, reason TargetClass) {
		t.Helper()
		var refusal *TargetProtectedError
		if !errors.As(err, &refusal) || refusal.Reason != reason {
			t.Fatalf("error = %v, want target protected (%s)", err, reason)
		}
	}

	// The second administrator is not the owner: it cannot touch the owner, the
	// platform account, itself or the owner's peers, only ordinary members.
	// Using the owner as the actor for these would hide the bug, so the second
	// administrator is the actor.
	assertProtected(suspend(f.adminID, f.ownerID), TargetOrganizationOwner)
	assertProtected(suspend(f.adminID, f.platformID), TargetPlatformAccount)
	assertProtected(suspend(f.adminID, f.adminID), TargetSelf)
	f.assertMembershipStatusAndRevision(t, f.orgA, f.ownerID, "active", f.revision(t, f.ownerID))
	if got := f.auditCount(t, "people.membership_update_refused", "conflict"); got != 3 {
		t.Fatalf("refusal audit rows = %d, want 3", got)
	}

	// The owner may suspend another administrator but not itself.
	assertProtected(suspend(f.ownerID, f.ownerID), TargetSelf)
	if err := suspend(f.ownerID, f.adminID); err != nil {
		t.Fatalf("owner suspends administrator: %v", err)
	}
	f.assertMembershipStatusAndRevision(t, f.orgA, f.adminID, "suspended", f.revision(t, f.adminID))

	// An administrator that is no longer active has no authority at all.
	if _, err := f.service.UpdateMembership(f.ctx, f.orgA, f.adminID, f.memberID, f.revision(t, f.memberID), tenancy.MembershipSuspended); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("suspended administrator acted: %v", err)
	}

	// An ordinary member is still within reach of an administrator.
	if _, err := f.service.UpdateMembership(f.ctx, f.orgA, f.ownerID, f.memberID, f.revision(t, f.memberID), tenancy.MembershipSuspended); err != nil {
		t.Fatalf("owner suspends member: %v", err)
	}
}

func TestUpdateMembershipByPlatformAdministrator(t *testing.T) {
	f := newAuthorityFixture(t)
	// A platform administrator who is not the server Owner.
	if _, err := f.pool.Exec(f.ctx, `UPDATE users SET role='admin' WHERE id=$1`, f.platformID); err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(f.ctx, MutationActor{AccountID: f.platformID, Authority: AuthorityPlatformAdmin})
	// May act on an administrator of the organization, never on itself, and may
	// not suspend the owner (nobody may).
	if _, err := f.service.UpdateMembership(ctx, f.orgA, f.platformID, f.adminID, f.revision(t, f.adminID), tenancy.MembershipSuspended); err != nil {
		t.Fatalf("platform administrator suspends organization administrator: %v", err)
	}
	if _, err := f.service.UpdateMembership(ctx, f.orgA, f.platformID, f.platformID, f.revision(t, f.platformID), tenancy.MembershipSuspended); !errors.Is(err, ErrTargetProtected) {
		t.Fatalf("platform administrator suspends itself: %v", err)
	}
	if _, err := f.service.UpdateMembership(ctx, f.orgA, f.platformID, f.ownerID, f.revision(t, f.ownerID), tenancy.MembershipSuspended); !errors.Is(err, ErrInvalidBulkAction) {
		t.Fatalf("platform administrator suspends owner: %v, want the standing owner refusal", err)
	}
}

func TestUpdateProfileGroupFollowsAuthorityRules(t *testing.T) {
	f := newAuthorityFixture(t)
	other := f.addGroup(t, f.orgA, "Second Group", false)
	move := func(actor, target int, profile string) error {
		_, err := f.service.UpdateProfileGroup(f.ctx, f.orgA, actor, target, profile, f.revision(t, target), other)
		return err
	}
	if err := move(f.adminID, f.ownerID, f.addProfile(t, f.ownerID, f.orgA, "Owner Profile", f.groupA)); !errors.Is(err, ErrTargetProtected) {
		t.Fatalf("administrator moves owner profile: %v", err)
	}
	if err := move(f.ownerID, f.adminID, f.adminProfile); err != nil {
		t.Fatalf("owner moves administrator profile: %v", err)
	}
	f.assertProfileGroup(t, f.adminID, f.adminProfile, other)
	if err := move(f.adminID, f.memberID, f.memberProfile); err != nil {
		t.Fatalf("administrator moves member profile: %v", err)
	}
	f.assertProfileGroup(t, f.memberID, f.memberProfile, other)
}

func TestBulkJobSkipsPeopleTheActorMayNotActOn(t *testing.T) {
	f := newAuthorityFixture(t)
	// The second administrator suspends everyone in the organization: only the
	// ordinary member goes through. The owner, the platform account and itself
	// are skipped, and nobody is suspended by accident.
	selection, err := f.service.CreateSelection(f.ctx, f.orgA, Filter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(f.ctx, MutationActor{AccountID: f.adminID, Authority: AuthorityOrganizationAdmin})
	queued, err := f.service.EnqueueBulk(ctx, f.orgA, f.adminID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.ProcessBulkJob(ctx, f.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{f.ownerID, f.platformID, f.adminID, f.sharedAccountID} {
		want := "active"
		if id == f.sharedAccountID {
			want = "suspended"
		}
		f.assertMembershipStatusAndRevision(t, f.orgA, id, want, f.revision(t, id))
	}
	f.assertMembershipStatusAndRevision(t, f.orgA, f.memberID, "suspended", f.revision(t, f.memberID))
	protected := map[int]string{}
	for _, record := range result.Skipped {
		protected[record.AccountID] = record.Reason
	}
	if protected[f.ownerID] != ReasonTargetProtected || protected[f.platformID] != ReasonTargetProtected || protected[f.adminID] != ReasonActorAuthorityTarget {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
}

func TestBudgetsCountPerPersonPerActorAndPerOrganization(t *testing.T) {
	f := newAuthorityFixture(t)
	record := func(actor int, action string, subject int, count int) {
		t.Helper()
		for i := 0; i < count; i++ {
			if _, err := f.pool.Exec(f.ctx, `INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome) VALUES ($1,'organization_admin','organization',$2,'membership','m',$3,$4,0,0,'success')`, actor, action, f.orgA, strconv.Itoa(subject)); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(budget Budget, actor, subject int) error {
		return CheckBudget(f.ctx, f.pool, budget, f.orgA, actor, strconv.Itoa(subject))
	}
	var limited *RateLimitedError

	// Per person: one reset in two minutes.
	if err := check(BudgetDisclose, f.ownerID, f.memberID); err != nil {
		t.Fatal(err)
	}
	record(f.ownerID, "people.password_reset_issued", f.memberID, 1)
	if err := check(BudgetDisclose, f.ownerID, f.memberID); !errors.As(err, &limited) || limited.RetryAfter <= 0 || limited.RetryAfter > 2*time.Minute {
		t.Fatalf("second reset for one person = %v", err)
	}
	if err := check(BudgetDisclose, f.ownerID, f.adminID); err != nil {
		t.Fatalf("another person is limited too: %v", err)
	}

	// Refusals count: a probe for protected people spends the same budget.
	record(f.ownerID, "people.password_reset_refused", f.adminID, 1)
	if err := check(BudgetDisclose, f.ownerID, f.adminID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("a refusal did not count: %v", err)
	}

	// Per actor: ten in ten minutes, across different people.
	record(f.adminID, "people.password_reset_issued", f.memberID+100, 9)
	if err := check(BudgetDisclose, f.adminID, f.memberID+200); err != nil {
		t.Fatalf("nine resets already limited the actor: %v", err)
	}
	record(f.adminID, "people.password_reset_issued", f.memberID+101, 1)
	if err := check(BudgetDisclose, f.adminID, f.memberID+200); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ten resets did not limit the actor: %v", err)
	}

	// Per organization, and only this organization.
	record(f.platformID, "people.profile_renamed", f.memberID, 6)
	if err := check(BudgetMutate, f.ownerID, f.memberID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("six edits of one person did not limit the sixth: %v", err)
	}
	if err := CheckBudget(f.ctx, f.pool, BudgetMutate, f.orgB, f.ownerBID, strconv.Itoa(f.memberID)); err != nil {
		t.Fatalf("another organization is limited: %v", err)
	}

	// Outside the window nothing counts.
	if _, err := f.pool.Exec(f.ctx, `ALTER TABLE admin_audit_events DISABLE TRIGGER admin_audit_events_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE admin_audit_events SET created_at = created_at - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if err := check(BudgetMutate, f.ownerID, f.memberID); err != nil {
		t.Fatalf("old events still count: %v", err)
	}
}

// TestPromotionAfterTheWriteRetiresTheResetLink pins the lock story: a write
// holds the member's row; a promotion waits for it and then, in its own
// transaction, deletes the link the write stored.
func TestPromotionAfterTheWriteRetiresTheResetLink(t *testing.T) {
	f := newAuthorityFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	actor := MutationActor{AccountID: f.adminID, Authority: AuthorityOrganizationAdmin}
	locked, err := LockAuthority(f.ctx, tx, f.orgA, actor, f.memberID)
	if err != nil || locked.Class != TargetMember {
		t.Fatalf("lock = %+v, %v", locked, err)
	}
	if _, err := tx.Exec(f.ctx, `INSERT INTO password_reset_tokens (user_id,token_hash,password_fingerprint,issued_by,expires_at) VALUES ($1,'digest','fingerprint',$2,now()+interval '1 hour')`, f.memberID, f.adminID); err != nil {
		t.Fatal(err)
	}
	promoted := make(chan error, 1)
	go func() {
		_, err := f.pool.Exec(f.ctx, `UPDATE organization_memberships SET legacy_role='admin' WHERE organization_id=$1 AND account_id=$2`, f.orgA, f.memberID)
		promoted <- err
	}()
	select {
	case err := <-promoted:
		t.Fatalf("the promotion did not wait for the write's lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-promoted; err != nil {
		t.Fatal(err)
	}
	var links int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id=$1`, f.memberID).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links after a later promotion = %d (%v)", links, err)
	}
}

// TestProfileGroupMoveRecordsAnOrganizationRevocation confirms finding F-3 of
// the organization support design: moving a profile to another access group
// raises the person's security revision, and that records an organization
// revocation, which ends the person's older account-login sessions at their
// next profile selection. The behavior is Silo-era and unchanged here; the
// support routes avoid it by using the per-profile revision for their edits.
func TestProfileGroupMoveRecordsAnOrganizationRevocation(t *testing.T) {
	f := newAuthorityFixture(t)
	other := f.addGroup(t, f.orgA, "Second Group", false)
	revocations := func() int {
		var count int
		if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM bloem_org_session_revocations WHERE account_id=$1 AND organization_id=$2`, f.memberID, f.orgA).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := revocations(); got != 0 {
		t.Fatalf("revocations before the move = %d", got)
	}
	if _, err := f.service.UpdateProfileGroup(f.ctx, f.orgA, f.ownerID, f.memberID, f.memberProfile, f.revision(t, f.memberID), other); err != nil {
		t.Fatal(err)
	}
	if got := revocations(); got != 1 {
		t.Fatalf("revocations after a group move = %d, want 1: the move signs the member out", got)
	}
}
