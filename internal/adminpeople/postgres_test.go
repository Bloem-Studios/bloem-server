package adminpeople

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestServiceListPeopleNeverReturnsForeignMemberships(t *testing.T) {
	fixture := newPeopleFixture(t)

	page, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Query: "shared@", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AccountID != fixture.sharedAccountID {
		t.Fatalf("people = %+v", page.Items)
	}
	person := page.Items[0]
	if person.OrganizationID != fixture.orgA || len(person.Profiles) != 1 || person.Profiles[0].ID != fixture.profileA || person.Profiles[0].GroupID != fixture.groupA {
		t.Fatalf("foreign profile or group leaked: %+v", person)
	}
	foreignName, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Query: "Foreign Profile", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(foreignName.Items) != 0 {
		t.Fatalf("foreign profile matched organization A: %+v", foreignName.Items)
	}
	foreignGroup, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{GroupIDs: []int{fixture.groupB}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(foreignGroup.Items) != 0 {
		t.Fatalf("foreign group matched organization A: %+v", foreignGroup.Items)
	}
}

func TestServiceListUsesStableOrganizationBoundCursor(t *testing.T) {
	fixture := newPeopleFixture(t)
	fixture.addAccount(t, fixture.orgA, "able@example.test", "Able", "Able Profile", fixture.groupA, false)
	fixture.addAccount(t, fixture.orgA, "zulu@example.test", "Zulu", "Zulu Profile", fixture.groupA, false)

	first, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Limit: 1, Sort: SortName})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Limit: 1, Sort: SortName, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].AccountID == first.Items[0].AccountID {
		t.Fatalf("second page = %+v after %+v", second, first)
	}
	if _, err := fixture.service.List(fixture.ctx, fixture.orgB, Filter{Limit: 1, Sort: SortName, Cursor: first.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("foreign cursor error = %v, want ErrInvalidCursor", err)
	}
}

func TestServiceCursorRejectsTamperingAndDifferentCanonicalFilters(t *testing.T) {
	fixture := newPeopleFixture(t)
	fixture.addAccount(t, fixture.orgA, "able@example.test", "Able", "Able Profile", fixture.groupA, false)
	first, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Query: "example.test", Limit: 1, Sort: SortName})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("missing next cursor")
	}
	replacement := "A"
	if strings.HasSuffix(first.NextCursor, replacement) {
		replacement = "B"
	}
	tampered := first.NextCursor[:len(first.NextCursor)-1] + replacement
	if _, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{Query: "example.test", Limit: 1, Sort: SortName, Cursor: tampered}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	for _, filter := range []Filter{
		{Query: "different", Limit: 1, Sort: SortName, Cursor: first.NextCursor},
		{Query: "example.test", Status: []tenancy.MembershipStatus{tenancy.MembershipSuspended}, Limit: 1, Sort: SortName, Cursor: first.NextCursor},
		{Query: "example.test", GroupIDs: []int{fixture.groupA}, Limit: 1, Sort: SortName, Cursor: first.NextCursor},
	} {
		if _, err := fixture.service.List(fixture.ctx, fixture.orgA, filter); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("changed filter %+v error = %v", filter, err)
		}
	}
}

func TestServiceSelectionPersistsCanonicalSnapshotServerSideAndIsImmutable(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{
		Query:    "  SHARED@Example.Test  ",
		Status:   []tenancy.MembershipStatus{tenancy.MembershipSuspended, tenancy.MembershipActive, tenancy.MembershipActive},
		GroupIDs: []int{fixture.groupA, fixture.groupA},
		Sort:     SortName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Token) > 128 || selection.Matched != 1 || selection.Excluded != 0 {
		t.Fatalf("selection = %+v", selection)
	}
	reference, err := fixture.service.parseSelectionReference(selection.Token)
	if err != nil {
		t.Fatal(err)
	}
	var organizationID uuid.UUID
	var canonical string
	var ids []int
	var snapshot, expires time.Time
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT organization_id,canonical_filter::text,account_ids,snapshot_at,expires_at FROM admin_people_selections WHERE id=$1`, reference).Scan(&organizationID, &canonical, &ids, &snapshot, &expires); err != nil {
		t.Fatal(err)
	}
	if organizationID != fixture.orgA || len(ids) != 1 || ids[0] != fixture.sharedAccountID || !strings.Contains(canonical, `"query": "shared@example.test"`) || !expires.Equal(snapshot.Add(selectionTTL)) {
		t.Fatalf("stored selection = org %s filter %s ids %v snapshot %s expires %s", organizationID, canonical, ids, snapshot, expires)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_people_selections SET matched_count=0 WHERE id=$1`, reference); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable selection update error = %v", err)
	}
	if _, err := fixture.service.ExecuteBulk(fixture.ctx, fixture.orgB, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("cross-organization selection error = %v, want ErrInvalidSelection", err)
	}
	fixture.service.now = func() time.Time { return expires.Add(time.Second) }
	if _, err := fixture.service.ExecuteBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships}); !errors.Is(err, ErrSelectionExpired) {
		t.Fatalf("expired selection error = %v, want ErrSelectionExpired", err)
	}
}

func TestServiceCleanupRemovesExpiredSelectionsEvenAfterUse(t *testing.T) {
	fixture := newPeopleFixture(t)
	active, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	activeID, _ := fixture.service.parseSelectionReference(active.Token)
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM admin_people_selections WHERE id=$1`, activeID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("unexpired delete error = %v", err)
	}
	fixture.service.now = func() time.Time { return time.Now().UTC().Add(-selectionTTL - time.Minute) }
	referenced, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	referencedID, _ := fixture.service.parseSelectionReference(referenced.Token)
	queued, err := fixture.service.EnqueueBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: referenced.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	expiredID, _ := fixture.service.parseSelectionReference(expired.Token)
	removed, err := fixture.service.CleanupExpiredSelections(fixture.ctx, 10)
	if err != nil || removed != 2 {
		t.Fatalf("cleanup = %d, %v", removed, err)
	}
	var activeExists, referencedExists, expiredExists bool
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT EXISTS(SELECT 1 FROM admin_people_selections WHERE id=$1),EXISTS(SELECT 1 FROM admin_people_selections WHERE id=$2),EXISTS(SELECT 1 FROM admin_people_selections WHERE id=$3)`, activeID, referencedID, expiredID).Scan(&activeExists, &referencedExists, &expiredExists); err != nil {
		t.Fatal(err)
	}
	if !activeExists || referencedExists || expiredExists {
		t.Fatalf("selection existence active=%t referenced=%t expired=%t", activeExists, referencedExists, expiredExists)
	}
	retried, err := fixture.service.EnqueueBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: referenced.Token, Kind: BulkSuspendMemberships})
	if err != nil || retried.JobID != queued.JobID {
		t.Fatalf("idempotent retry after selection cleanup = %+v, %v", retried, err)
	}
}

func TestEnqueueBulkInTransactionRollsBackWithCaller(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueueBulkInTransaction(fixture.ctx, tx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		_ = tx.Rollback(fixture.ctx)
		t.Fatal(err)
	}
	var visible bool
	if err := tx.QueryRow(fixture.ctx, `SELECT EXISTS(SELECT 1 FROM admin_jobs WHERE id=$1)`, queued.JobID).Scan(&visible); err != nil || !visible {
		_ = tx.Rollback(fixture.ctx)
		t.Fatalf("job visible in caller transaction = %t, %v", visible, err)
	}
	if err := tx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT EXISTS(SELECT 1 FROM admin_jobs WHERE id=$1)`, queued.JobID).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible {
		t.Fatalf("job %s survived caller rollback", queued.JobID)
	}
}

func TestServiceCleanupTerminalBulkJobsIsBoundedAndPreservesActiveJobs(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin})
	completed, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	active, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkReactivateMemberships})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkAssignGroup, GroupID: &fixture.groupA})
	if err != nil {
		t.Fatal(err)
	}
	recentSelection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	recent, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: recentSelection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_jobs SET status='completed',completed_at=now()-interval '25 hours' WHERE id=$1`, completed.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_jobs SET status='failed',completed_at=now()-interval '25 hours' WHERE id=$1`, failed.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_jobs SET status='completed',completed_at=now() WHERE id=$1`, recent.JobID); err != nil {
		t.Fatal(err)
	}
	removed, err := fixture.service.CleanupTerminalBulkJobs(fixture.ctx, time.Now().UTC().Add(-24*time.Hour), 1)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup terminal jobs = %d, %v", removed, err)
	}
	if _, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, completed.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed job lookup error = %v", err)
	}
	removed, err = fixture.service.CleanupTerminalBulkJobs(fixture.ctx, time.Now().UTC().Add(-24*time.Hour), 10)
	if err != nil || removed != 1 {
		t.Fatalf("second terminal cleanup = %d, %v", removed, err)
	}
	if _, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, failed.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed job lookup error = %v", err)
	}
	if got, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, active.JobID); err != nil || got.Status != "queued" {
		t.Fatalf("active job = %+v, %v", got, err)
	}
	if got, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, recent.JobID); err != nil || got.Status != "completed" {
		t.Fatalf("recent terminal job = %+v, %v", got, err)
	}
}

func TestServiceBulkActorAuthorityLocksSerializeConcurrentRevocation(t *testing.T) {
	tests := []struct {
		name      string
		platform  bool
		revokeSQL string
	}{
		{name: "membership suspension", revokeSQL: `UPDATE organization_memberships SET status='suspended',security_revision=security_revision+1 WHERE id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`},
		{name: "membership demotion", revokeSQL: `UPDATE organization_memberships SET legacy_role='user',security_revision=security_revision+1 WHERE id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`},
		{name: "organization policy", revokeSQL: `UPDATE organizations SET policy_revision=policy_revision+1 WHERE id=$1`},
		{name: "platform disable", platform: true, revokeSQL: `UPDATE users SET enabled=false WHERE id=$1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPeopleFixture(t)
			authority := AuthorityOrganizationAdmin
			revokeArg := any(fixture.ownerMembershipID)
			if test.name == "organization policy" {
				revokeArg = fixture.orgA
			}
			if test.platform {
				authority = AuthorityPlatformAdmin
				revokeArg = fixture.ownerID
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
					t.Fatal(err)
				}
			}
			selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: authority, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
			queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
			if err != nil {
				t.Fatal(err)
			}
			revoker, err := fixture.pool.Begin(fixture.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer revoker.Rollback(fixture.ctx)
			if _, err := revoker.Exec(fixture.ctx, test.revokeSQL, revokeArg); err != nil {
				t.Fatal(err)
			}
			type processOutcome struct {
				result BulkResult
				err    error
			}
			processed := make(chan processOutcome, 1)
			go func() {
				result, err := fixture.service.ProcessBulkBatch(fixture.ctx, fixture.orgA, queued.JobID, 1)
				processed <- processOutcome{result: result, err: err}
			}()
			select {
			case outcome := <-processed:
				t.Fatalf("processing did not wait for authority transaction: %+v, %v", outcome.result, outcome.err)
			case <-time.After(200 * time.Millisecond):
			}
			fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.sharedAccountID, "active", 1)
			if err := revoker.Commit(fixture.ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case outcome := <-processed:
				if !errors.Is(outcome.err, ErrAuthorizationStateChanged) {
					t.Fatalf("processing error = %v", outcome.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("processing remained blocked after revocation commit")
			}
			fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.sharedAccountID, "active", 1)
		})
	}
}

func TestServiceBulkSkipsOrganizationAdminActorTarget(t *testing.T) {
	fixture := newPeopleFixture(t)
	actorID, _ := fixture.addAccount(t, fixture.orgA, "secondary-admin@example.test", "Secondary Admin", "Secondary Admin", fixture.groupA, true)
	var membershipID uuid.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT id FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, actorID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "secondary-admin@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: actorID, Authority: AuthorityOrganizationAdmin, MembershipID: membershipID, SecurityRevision: 1, PolicyRevision: 1})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, actorID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].AccountID != actorID || result.Skipped[0].Reason != ReasonActorAuthorityTarget {
		t.Fatalf("result=%+v", result)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, actorID, "active", 1)
}

func TestServiceBulkJobIsDurableResumableAndIdempotent(t *testing.T) {
	fixture := newPeopleFixture(t)
	regularID, _ := fixture.addAccount(t, fixture.orgA, "regular@example.test", "Regular", "Regular Profile", fixture.groupA, false)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, RequestID: "durable-job"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" || queued.ProgressCurrent != 0 || queued.ProgressTotal < 2 {
		t.Fatalf("queued job = %+v", queued)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_people_bulk_jobs SET action_kind='reactivate_memberships' WHERE job_id=$1`, queued.JobID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable action update error=%v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_people_bulk_targets SET snapshot='{}'::jsonb WHERE job_id=$1`, queued.JobID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable target snapshot update error=%v", err)
	}
	again, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil || again.JobID != queued.JobID {
		t.Fatalf("idempotent enqueue = %+v, %v", again, err)
	}
	one, err := fixture.service.ProcessBulkBatch(ctx, fixture.orgA, queued.JobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if one.Status != "running" || one.ProgressCurrent != 1 {
		t.Fatalf("first batch = %+v", one)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := fixture.service.ProcessBulkJob(canceled, fixture.orgA, queued.JobID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled process error = %v", err)
	}
	observed, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, queued.JobID)
	if err != nil || observed.Status != "running" || observed.ProgressCurrent != 1 {
		t.Fatalf("observed after cancellation = %+v, %v", observed, err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.ProgressCurrent != completed.ProgressTotal {
		t.Fatalf("completed = %+v", completed)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, regularID, "suspended", 2)
	replayed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || replayed.Succeeded != completed.Succeeded {
		t.Fatalf("replayed = %+v, %v", replayed, err)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, regularID, "suspended", 2)
}

func TestServiceConcurrentBulkEnqueueCreatesOneDurableJob(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, RequestID: "concurrent-enqueue"})
	action := BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships}
	type outcome struct {
		result BulkResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			result, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, action)
			results <- outcome{result, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("enqueue errors = %v / %v", first.err, second.err)
	}
	if first.result.JobID != second.result.JobID {
		t.Fatalf("job IDs = %q / %q", first.result.JobID, second.result.JobID)
	}
	var jobs int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_people_bulk_jobs WHERE selection_reference=(SELECT id FROM admin_people_selections LIMIT 1)`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("durable jobs=%d", jobs)
	}
}

func TestServiceListRunnableBulkJobsRecoversQueuedAndRunning(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_jobs SET status='running' WHERE id=$1`, queued.JobID); err != nil {
		t.Fatal(err)
	}
	jobs, err := fixture.service.ListRunnableBulkJobs(fixture.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].JobID != queued.JobID || jobs[0].OrganizationID != fixture.orgA {
		t.Fatalf("jobs=%+v", jobs)
	}
}

func TestServiceBulkJobFailsBeforeMutationWhenOrganizationAuthorityRevoked(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "actor-revoked"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET status='suspended',security_revision=security_revision+1 WHERE id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.ownerMembershipID); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ProcessBulkJob(fixture.ctx, fixture.orgA, queued.JobID)
	if err == nil {
		t.Fatal("ProcessBulkJob error=nil")
	}
	observed, getErr := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, queued.JobID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if result.JobID != "" || observed.Status != "failed" {
		t.Fatalf("result=%+v observed=%+v", result, observed)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.sharedAccountID, "active", 1)
	var audits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE request_id='actor-revoked' AND action='people.bulk_job_failed'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("failure audits=%d", audits)
	}
}

func TestServiceBulkJobFailsBeforeMutationWhenPlatformAuthorityRevoked(t *testing.T) {
	fixture := newPeopleFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='user' WHERE id=$1`, fixture.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ProcessBulkJob(fixture.ctx, fixture.orgA, queued.JobID); err == nil {
		t.Fatal("ProcessBulkJob error=nil")
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.sharedAccountID, "active", 1)
}

func TestServiceCreateSelectionCountsAndTargetsShareOneSnapshot(t *testing.T) {
	fixture := newPeopleFixture(t)
	before, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.selectionSnapshotHook = func() {
		fixture.addAccount(t, fixture.orgA, "snapshot-late@example.test", "Snapshot Late", "Snapshot Late", fixture.groupA, false)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.selectionSnapshotHook = nil
	after, err := fixture.service.List(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if after.ApproximateTotal != before.ApproximateTotal+1 || selection.Matched != before.ApproximateTotal {
		t.Fatalf("selection matched=%d before=%d after=%d", selection.Matched, before.ApproximateTotal, after.ApproximateTotal)
	}
	reference, _ := fixture.service.parseSelectionReference(selection.Token)
	var matched, excluded int64
	var targetCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT matched_count,excluded_count,jsonb_array_length(targets) FROM admin_people_selections WHERE id=$1`, reference).Scan(&matched, &excluded, &targetCount); err != nil {
		t.Fatal(err)
	}
	if matched < 0 || excluded < 0 || matched != int64(targetCount) || selection.Matched != matched || selection.Excluded != excluded {
		t.Fatalf("selection=%+v stored matched=%d excluded=%d targets=%d", selection, matched, excluded, targetCount)
	}
}

func TestServiceCreateSelectionRequiresEveryExplicitAccountInOrganization(t *testing.T) {
	fixture := newPeopleFixture(t)
	var foreignOnlyID int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT m.account_id
		FROM organization_memberships m
		WHERE m.organization_id=$1
		  AND NOT EXISTS (
			SELECT 1 FROM organization_memberships own
			WHERE own.organization_id=$2 AND own.account_id=m.account_id
		  )
		ORDER BY m.account_id
		LIMIT 1`, fixture.orgB, fixture.orgA).Scan(&foreignOnlyID); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{
		AccountIDs: []int{fixture.sharedAccountID, foreignOnlyID}, RequireAllAccountIDs: true,
		Status: []tenancy.MembershipStatus{tenancy.MembershipActive},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign explicit account error = %v, want ErrNotFound", err)
	}

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{
		AccountIDs: []int{fixture.sharedAccountID, fixture.ownerID}, RequireAllAccountIDs: true,
		Status: []tenancy.MembershipStatus{tenancy.MembershipActive},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Matched != 2 || selection.Excluded != 0 {
		t.Fatalf("selection = %#v, want exactly two scoped accounts", selection)
	}
}

func TestServiceBulkSkipsAuthorizationStateChangedAfterSelection(t *testing.T) {
	fixture := newPeopleFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, RequestID: "concurrent-change"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET security_revision=security_revision+1 WHERE organization_id=$1 AND account_id=$2 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.orgA, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Skipped) != 1 || completed.Skipped[0].AccountID != fixture.sharedAccountID || completed.Skipped[0].Reason != ReasonAuthorizationChanged {
		t.Fatalf("result = %+v", completed)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.sharedAccountID, "active", 2)
	var authority string
	var skipped, created, finished int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT max(actor_platform_role),count(*) FILTER (WHERE action='people.bulk_record_skipped'),count(*) FILTER (WHERE action='people.bulk_job_created'),count(*) FILTER (WHERE action='people.bulk_job_completed') FROM admin_audit_events WHERE request_id='concurrent-change'`).Scan(&authority, &skipped, &created, &finished); err != nil {
		t.Fatal(err)
	}
	if authority != AuthorityPlatformAdmin || skipped != 1 || created != 1 || finished != 1 {
		t.Fatalf("audits authority=%q skipped=%d created=%d completed=%d", authority, skipped, created, finished)
	}
}

func TestServiceBulkCompletionAuditMarksPartialFailure(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	reference, _ := fixture.service.parseSelectionReference(selection.Token)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_people_selections SET expires_at=expires_at WHERE id=$1`, reference); err == nil {
		t.Fatal("selection unexpectedly mutable")
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "partial-audit"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	var vanished int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT account_id FROM admin_people_bulk_targets WHERE job_id=$1 AND account_id<>$2 LIMIT 1`, queued.JobID, fixture.ownerID).Scan(&vanished); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, vanished); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Failed) == 0 {
		t.Fatalf("completed=%+v", completed)
	}
	var outcome string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT outcome FROM admin_audit_events WHERE request_id='partial-audit' AND action='people.bulk_job_completed'`).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "partial_failure" {
		t.Fatalf("completion outcome=%q", outcome)
	}
}

func TestServiceBulkGroupAssignmentSkipsProfileChangedAfterSelection(t *testing.T) {
	fixture := newPeopleFixture(t)
	targetGroup := fixture.addGroup(t, fixture.orgA, "Target", false)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, RequestID: "profile-change"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkAssignGroup, GroupID: &targetGroup})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET updated_at=updated_at+interval '1 second' WHERE organization_id=$1 AND user_id=$2 AND id=$3`, fixture.orgA, fixture.sharedAccountID, fixture.profileA); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Skipped) != 1 || completed.Skipped[0].Reason != ReasonAuthorizationChanged {
		t.Fatalf("result = %+v", completed)
	}
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileA, fixture.groupA)
}

func TestServiceBulkJobRecordsFatalExecutorFailure(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, RequestID: "fatal-job"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `ALTER TABLE admin_people_bulk_targets DISABLE TRIGGER admin_people_bulk_target_snapshot_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE admin_people_bulk_targets SET snapshot='{}'::jsonb WHERE job_id=$1`, queued.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `ALTER TABLE admin_people_bulk_targets ENABLE TRIGGER admin_people_bulk_target_snapshot_immutable`); err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err == nil {
		t.Fatal("ProcessBulkJob() error=nil, want malformed durable target failure")
	}
	observed, getErr := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, queued.JobID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if failed.JobID != "" || observed.Status != "failed" {
		t.Fatalf("returned=%+v observed=%+v processErr=%v", failed, observed, err)
	}
	var audits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE request_id='fatal-job' AND action='people.bulk_job_failed' AND outcome='failure'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("failure audits=%d", audits)
	}
}

func TestServiceBulkGroupAssignmentIsOrganizationBoundAndRevisionGuarded(t *testing.T) {
	fixture := newPeopleFixture(t)
	groupA2 := fixture.addGroup(t, fixture.orgA, "A Restricted", false)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.ExecuteBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkAssignGroup, GroupID: &groupA2})
	if err != nil {
		t.Fatal(err)
	}
	result, err = fixture.service.ProcessBulkJob(fixture.ctx, fixture.orgA, result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 1 || len(result.Skipped) != 0 || len(result.Failed) != 0 || result.JobID == "" {
		t.Fatalf("bulk result = %+v", result)
	}
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileA, groupA2)
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileB, fixture.groupB)
	fixture.assertMembershipRevision(t, fixture.orgA, fixture.sharedAccountID, 2)
	fixture.assertMembershipRevision(t, fixture.orgB, fixture.sharedAccountID, 1)
	fixture.assertAccountPolicyRevision(t, fixture.sharedAccountID, 2)
	stored, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, result.JobID)
	if err != nil || stored.JobID != result.JobID || stored.Succeeded != 1 {
		t.Fatalf("stored job = %+v, %v", stored, err)
	}
	if _, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgB, result.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign job error = %v, want ErrNotFound", err)
	}
	if _, err := fixture.service.ExecuteBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkAssignGroup, GroupID: &fixture.groupB}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign group error = %v, want ErrNotFound", err)
	}
	var audits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE organization_id=$1 AND action='people.bulk_group_assigned'`, fixture.orgA).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
	var authority string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT actor_platform_role FROM admin_audit_events WHERE organization_id=$1 AND action='people.bulk_job_completed' ORDER BY id DESC LIMIT 1`, fixture.orgA).Scan(&authority); err != nil {
		t.Fatal(err)
	}
	if authority != AuthorityOrganizationAdmin {
		t.Fatalf("organization audit authority=%q", authority)
	}
}

func TestServiceBulkSuspensionReturnsExactPartialResultAndRevokesContext(t *testing.T) {
	fixture := newPeopleFixture(t)
	regularID, _ := fixture.addAccount(t, fixture.orgA, "regular@example.test", "Regular", "Regular Profile", fixture.groupA, false)
	vanishedID, _ := fixture.addAccount(t, fixture.orgA, "vanished@example.test", "Vanished", "Vanished Profile", fixture.groupA, false)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Status: []tenancy.MembershipStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, vanishedID); err != nil {
		t.Fatal(err)
	}
	lateID, _ := fixture.addAccount(t, fixture.orgA, "late@example.test", "Late", "Late Profile", fixture.groupA, false)
	result, err := fixture.service.ExecuteBulk(fixture.ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	result, err = fixture.service.ProcessBulkJob(fixture.ctx, fixture.orgA, result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 2 { // shared and regular; the actor is excluded.
		t.Fatalf("succeeded = %d, result=%+v", result.Succeeded, result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].AccountID != fixture.ownerID || result.Skipped[0].Reason != ReasonActorAuthorityTarget {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
	if len(result.Failed) != 1 || result.Failed[0].AccountID != vanishedID || result.Failed[0].Reason != ReasonNotFound {
		t.Fatalf("failed = %+v", result.Failed)
	}
	var failedAudits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE organization_id=$1 AND action='people.bulk_record_failed' AND subject_id=$2 AND outcome='failure'`, fixture.orgA, strconv.Itoa(vanishedID)).Scan(&failedAudits); err != nil {
		t.Fatal(err)
	}
	if failedAudits != 1 {
		t.Fatalf("failed record audits=%d", failedAudits)
	}
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, regularID, "suspended", 2)
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, fixture.ownerID, "active", 1)
	fixture.assertMembershipStatusAndRevision(t, fixture.orgA, lateID, "active", 1)
}

func TestPolicyBulkJobMovesInheritedProfilesAndPreservesCustomProfiles(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Policy custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Custom policy", customGroup)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2 AND id<>$4`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID, customProfile); err != nil {
		t.Fatal(err)
	}

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyApplyEntitlementTemplate, TemplateKey: "premium", TemplateRevision: 1}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-exact"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
		SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
		IdempotencyKey: "policy-exact-1", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.Succeeded != 1 || completed.TargetCohortID != premium.ID || completed.TargetCohortRevision != premium.Revision || completed.TargetGroupID != premium.AccessGroupID {
		t.Fatalf("completed = %+v", completed)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, int(premium.AccessGroupID))
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileA, int(premium.AccessGroupID))
	fixture.assertProfileGroup(t, fixture.sharedAccountID, customProfile, customGroup)

	replayed, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
		SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
		IdempotencyKey: "policy-exact-1", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.JobID != completed.JobID || replayed.Succeeded != 1 {
		t.Fatalf("replayed = %+v, completed = %+v", replayed, completed)
	}
	var assignmentAudits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE request_id='policy-exact' AND action='people.bulk_policy_assigned'`).Scan(&assignmentAudits); err != nil {
		t.Fatal(err)
	}
	if assignmentAudits != 1 {
		t.Fatalf("assignment audits = %d, want 1", assignmentAudits)
	}
}

func TestPolicyBulkJobClearsManagedAccountOverridesAndAuditsEffectivePolicy(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Override custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Override custom profile", customGroup)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)
	execMembershipPolicy(t, fixture.ctx, fixture.pool, `
		UPDATE organization_memberships SET
			library_ids='{}'::integer[],max_playback_quality='720p',max_streams=1,max_transcodes=1,
			transcode_allowed=false,audio_transcode_allowed=false,download_allowed=false,
			download_transcode_allowed=false,requests_allowed=false
		WHERE organization_id=$2 AND account_id=$1`, fixture.sharedAccountID, fixture.orgA)

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-overrides"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
		SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
		IdempotencyKey: "policy-overrides", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}

	actual, err := store.GetAccountPolicy(fixture.ctx, fixture.orgA, fixture.sharedAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.GroupID != premium.AccessGroupID ||
		!reflect.DeepEqual(actual.Policy.LibraryIDs, preview.Target.Policy.LibraryIDs) ||
		actual.Policy.MaxStreams != preview.Target.Policy.MaxStreams ||
		actual.Policy.MaxTranscodes != preview.Target.Policy.MaxTranscodes ||
		actual.Policy.TranscodeAllowed != preview.Target.Policy.TranscodeAllowed ||
		actual.Policy.DownloadAllowed != preview.Target.Policy.DownloadAllowed ||
		actual.Policy.DownloadTranscodeAllowed != preview.Target.Policy.DownloadTranscodeAllowed ||
		actual.Policy.MaxPlaybackQuality != preview.Target.Policy.MaxPlaybackQuality ||
		actual.Policy.RequestsAllowed != preview.Target.Policy.RequestsAllowed {
		t.Fatalf("effective policy=%+v, want managed target=%+v", actual.Policy, preview.Target.Policy)
	}
	var managedOverridesCleared bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT library_ids IS NULL AND max_playback_quality IS NULL AND
		       max_streams IS NULL AND max_transcodes IS NULL AND
		       transcode_allowed IS NULL AND audio_transcode_allowed IS NULL AND
		       download_allowed IS NULL AND download_transcode_allowed IS NULL AND
		       requests_allowed IS NULL
		FROM organization_memberships WHERE organization_id=$2 AND account_id=$1`, fixture.sharedAccountID, fixture.orgA).Scan(&managedOverridesCleared); err != nil {
		t.Fatal(err)
	}
	if !managedOverridesCleared {
		t.Fatal("managed account overrides were not cleared")
	}
	fixture.assertProfileGroup(t, fixture.sharedAccountID, customProfile, customGroup)

	var audited entitlements.EffectivePolicySnapshot
	var auditedDigest string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT after_state->'effective_policy',after_state->>'effective_policy_digest'
		FROM admin_audit_events
		WHERE request_id='policy-overrides' AND action='people.bulk_policy_assigned'`).Scan(&audited, &auditedDigest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(audited, actual.Policy) {
		t.Fatalf("audited effective policy=%+v, want actual=%+v", audited, actual.Policy)
	}
	wantDigest, err := entitlements.EffectivePolicyDigest(actual.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if auditedDigest != wantDigest {
		t.Fatalf("audited effective policy digest=%q, want %q", auditedDigest, wantDigest)
	}
}

func TestPolicyBulkJobReconcilesOverridesOnAlreadyAssignedCohort(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), premium.AccessGroupID)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET max_streams=1 WHERE account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-reconcile-overrides"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-reconcile-overrides", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 || len(completed.Skipped) != 0 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	var maxStreams *int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT max_streams FROM organization_memberships WHERE account_id=$1`, fixture.sharedAccountID).Scan(&maxStreams); err != nil {
		t.Fatal(err)
	}
	if maxStreams != nil {
		t.Fatalf("max_streams override=%d, want inherited NULL", *maxStreams)
	}
	actual, err := store.GetAccountPolicy(fixture.ctx, fixture.orgA, fixture.sharedAccountID)
	if err != nil || actual.Policy.MaxStreams != preview.Target.Policy.MaxStreams {
		t.Fatalf("effective max_streams=%d err=%v, want %d", actual.Policy.MaxStreams, err, preview.Target.Policy.MaxStreams)
	}
}

func TestPolicyBulkJobReconcilesNonNullableMaxProfilesOnAlreadyAssignedCohort(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), premium.AccessGroupID)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET max_profiles=1 WHERE account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-reconcile-max-profiles"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if preview.AlreadyCompliant != 0 {
		t.Fatalf("already compliant=%d, want 0 for conflicting account max_profiles", preview.AlreadyCompliant)
	}

	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
		SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
		IdempotencyKey: "policy-reconcile-max-profiles", Command: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 || len(completed.Skipped) != 0 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}

	actual, err := store.GetAccountPolicy(fixture.ctx, fixture.orgA, fixture.sharedAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Policy.MaxProfiles != preview.Target.Policy.MaxProfiles {
		t.Fatalf("effective max_profiles=%d, want target %d", actual.Policy.MaxProfiles, preview.Target.Policy.MaxProfiles)
	}
	for _, profile := range actual.Profiles {
		if profile.InheritsAccount && profile.Policy.MaxProfiles != preview.Target.Policy.MaxProfiles {
			t.Fatalf("inherited profile %q max_profiles=%d, want target %d", profile.ProfileID, profile.Policy.MaxProfiles, preview.Target.Policy.MaxProfiles)
		}
	}
	var persisted int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT max_profiles FROM organization_memberships WHERE account_id=$1`, fixture.sharedAccountID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != preview.Target.Policy.MaxProfiles {
		t.Fatalf("persisted max_profiles=%d, want target %d", persisted, preview.Target.Policy.MaxProfiles)
	}
}

func TestPolicyBulkConfirmationRejectsCrossOperationScope(t *testing.T) {
	for _, test := range []struct {
		name         string
		previewScope PolicyOperationScope
		enqueueScope PolicyOperationScope
	}{
		{name: "organization to direct accounts", previewScope: PolicyOperationScopeOrganization, enqueueScope: PolicyOperationScopeDirectAccounts},
		{name: "direct accounts to organization", previewScope: PolicyOperationScopeDirectAccounts, enqueueScope: PolicyOperationScopeOrganization},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPeopleFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
				t.Fatal(err)
			}
			selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			command := PolicyCommand{Kind: PolicyRestoreDefaultEntitlement}
			ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, SecurityRevision: 1, PolicyRevision: 1})
			preview, err := fixture.service.PreviewPolicyForScope(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, test.previewScope)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.service.EnqueuePolicyBulkForScope(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
				SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
				IdempotencyKey: "cross-scope-confirmation", Command: command,
			}, test.enqueueScope)
			if !errors.Is(err, ErrInvalidPolicyConfirmation) {
				t.Fatalf("cross-scope confirmation error = %v, want ErrInvalidPolicyConfirmation", err)
			}
		})
	}
}

func TestEnqueuePolicyBulkInTransactionRollsBackWithCaller(t *testing.T) {
	fixture := newPeopleFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyRestoreDefaultEntitlement}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicyForScope(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, PolicyOperationScopeOrganization)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulkForScopeInTransaction(ctx, tx, fixture.orgA, fixture.ownerID, PolicyBulkAction{
		SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken,
		IdempotencyKey: "caller-transaction-rollback", Command: command,
	}, PolicyOperationScopeOrganization)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var jobExists, receiptExists bool
	if err := fixture.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_jobs WHERE id=$1), EXISTS(SELECT 1 FROM admin_people_bulk_job_receipts WHERE job_id=$1)`, queued.JobID).Scan(&jobExists, &receiptExists); err != nil {
		t.Fatal(err)
	}
	if jobExists || receiptExists {
		t.Fatalf("caller rollback left job=%t receipt=%t", jobExists, receiptExists)
	}
}

func TestPolicyBulkIdempotencyRejectsCrossOperationScopeAndPreservesSameScopeReplay(t *testing.T) {
	for _, test := range []struct {
		name         string
		previewScope PolicyOperationScope
		replayScope  PolicyOperationScope
	}{
		{name: "organization to direct accounts", previewScope: PolicyOperationScopeOrganization, replayScope: PolicyOperationScopeDirectAccounts},
		{name: "direct accounts to organization", previewScope: PolicyOperationScopeDirectAccounts, replayScope: PolicyOperationScopeOrganization},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPeopleFixture(t)
			if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET role='admin' WHERE id=$1`, fixture.ownerID); err != nil {
				t.Fatal(err)
			}
			selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			command := PolicyCommand{Kind: PolicyRestoreDefaultEntitlement}
			ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, SecurityRevision: 1, PolicyRevision: 1})
			preview, err := fixture.service.PreviewPolicyForScope(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, test.previewScope)
			if err != nil {
				t.Fatal(err)
			}
			action := PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "cross-scope-replay", Command: command}
			queued, err := fixture.service.EnqueuePolicyBulkForScope(ctx, fixture.orgA, fixture.ownerID, action, test.previewScope)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := fixture.service.EnqueuePolicyBulkForScope(ctx, fixture.orgA, fixture.ownerID, action, test.previewScope)
			if err != nil || replayed.JobID != queued.JobID {
				t.Fatalf("same-scope replay = %+v, err=%v, want job %s", replayed, err, queued.JobID)
			}
			replayPreview, err := fixture.service.PreviewPolicyForScope(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, test.replayScope)
			if err != nil {
				t.Fatal(err)
			}
			action.ConfirmationToken = replayPreview.ConfirmationToken
			if _, err := fixture.service.EnqueuePolicyBulkForScope(ctx, fixture.orgA, fixture.ownerID, action, test.replayScope); !errors.Is(err, ErrBulkIdempotencyConflict) {
				t.Fatalf("cross-scope replay error = %v, want ErrBulkIdempotencyConflict", err)
			}
		})
	}
}

func TestPolicyBulkJobLookupAndCancelRejectGenericJobs(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	generic, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.GetPolicyBulkJob(ctx, fixture.orgA, generic.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("generic job policy lookup error=%v", err)
	}
	if _, err := fixture.service.CancelPolicyBulkJob(ctx, fixture.orgA, fixture.ownerID, generic.JobID); !errors.Is(err, ErrBulkJobNotCancellable) {
		t.Fatalf("generic job policy cancellation error=%v", err)
	}
	observed, err := fixture.service.GetBulkJob(ctx, fixture.orgA, generic.JobID)
	if err != nil || observed.Status != "queued" {
		t.Fatalf("generic job changed=%+v err=%v", observed, err)
	}
}

func TestPolicyBulkJobMovesCustomProfilesWhenConfirmed(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Confirmed custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Confirmed custom profile", customGroup)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID, IncludeCustomProfiles: true}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-custom-confirmed"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CustomProfilesWillMove != 1 || preview.CustomProfilesWillRemain != 0 {
		t.Fatalf("custom preview move/remain=%d/%d", preview.CustomProfilesWillMove, preview.CustomProfilesWillRemain)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-custom-confirmed", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, int(premium.AccessGroupID))
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileA, int(premium.AccessGroupID))
	fixture.assertProfileGroup(t, fixture.sharedAccountID, customProfile, int(premium.AccessGroupID))
}

func TestPolicyBulkJobRestartResumesWithoutRepeatingCompletedTargets(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	accountIDs := make([]int, 0, 3)
	for index := 1; index <= 3; index++ {
		accountID, _ := fixture.addAccount(t, fixture.orgA, fmt.Sprintf("policy-restart-%d@example.test", index), fmt.Sprintf("Policy Restart %d", index), fmt.Sprintf("Policy Restart Profile %d", index), int(standard.AccessGroupID), false)
		accountIDs = append(accountIDs, accountID)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=ANY($1::integer[]) AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, accountIDs, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "policy-restart-"})
	if err != nil || selection.Matched != 3 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-restart"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-restart", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.service.ProcessBulkBatch(ctx, fixture.orgA, queued.JobID, 1)
	if err != nil || first.Status != "running" || first.ProgressCurrent != 1 || first.Succeeded != 1 {
		t.Fatalf("first batch=%+v err=%v", first, err)
	}
	restarted := NewService(fixture.pool, "people-postgres-test-secret")
	completed, err := restarted.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Status != "completed" || completed.ProgressCurrent != 3 || completed.Succeeded != 3 {
		t.Fatalf("restarted completion=%+v err=%v", completed, err)
	}
	replayed, err := restarted.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || replayed.Succeeded != 3 {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	var assignmentAudits, attemptedOnce int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE action='people.bulk_policy_assigned' AND after_state->>'job_id'=$1`, queued.JobID).Scan(&assignmentAudits); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_people_bulk_targets WHERE job_id=$1 AND attempts=1 AND status='succeeded'`, queued.JobID).Scan(&attemptedOnce); err != nil {
		t.Fatal(err)
	}
	if assignmentAudits != 3 || attemptedOnce != 3 {
		t.Fatalf("assignment audits=%d attempted once=%d", assignmentAudits, attemptedOnce)
	}
}

func TestPolicyBulkJobRejectsPayloadMismatchAndCancelsWithoutEffects(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-cancel"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-cancel-1", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	changed := command
	changed.IncludeCustomProfiles = true
	if _, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-cancel-1", Command: changed}); !errors.Is(err, ErrBulkIdempotencyConflict) {
		t.Fatalf("payload mismatch error = %v", err)
	}
	canceled, err := fixture.service.CancelBulkJob(ctx, fixture.orgA, fixture.ownerID, queued.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != bulkStatusCancelled || canceled.ProgressCurrent != 0 {
		t.Fatalf("canceled = %+v", canceled)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, int(standard.AccessGroupID))
	replayed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || replayed.Status != bulkStatusCancelled {
		t.Fatalf("canceled replay = %+v, %v", replayed, err)
	}
}

func TestPolicyBulkJobFailureSchedulesSafeBackoff(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-backoff-1", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.FailBulkJob(ctx, fixture.orgA, queued.JobID, errors.New("secret database details")); err != nil {
		t.Fatal(err)
	}
	var status, safeError, storedCode string
	var attempts int
	var delayed bool
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT j.status,j.error_message,b.last_error_code,b.attempt_count,b.next_attempt_at>now() FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1`, queued.JobID).Scan(&status, &safeError, &storedCode, &attempts, &delayed); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || safeError != "executor_failure" || storedCode != "executor_failure" || attempts != 1 || !delayed {
		t.Fatalf("retry state = %q/%q/%q/%d/%v", status, safeError, storedCode, attempts, delayed)
	}
}

func TestPolicyBulkJobPrefetchedReplicaHonorsRetryBackoff(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-prefetched-backoff", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	prefetched, err := fixture.service.ListRunnableBulkJobs(ctx, 10)
	if err != nil || len(prefetched) != 1 || prefetched[0].JobID != queued.JobID {
		t.Fatalf("prefetched=%+v err=%v", prefetched, err)
	}
	if err := fixture.service.FailBulkJob(ctx, fixture.orgA, queued.JobID, errors.New("replica one failed")); err != nil {
		t.Fatal(err)
	}
	replica := NewService(fixture.pool, "people-postgres-test-secret")
	result, err := replica.ProcessBulkBatch(ctx, prefetched[0].OrganizationID, prefetched[0].JobID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "queued" || result.ProgressCurrent != 0 || result.Succeeded != 0 {
		t.Fatalf("backed-off prefetched result=%+v", result)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, int(standard.AccessGroupID))
}

func TestPolicyBulkJobFailureExhaustionWritesTerminalAudit(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-exhaustion"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-exhaustion", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	var previousRetry time.Time
	for attempt := 1; attempt <= 5; attempt++ {
		if err := fixture.service.FailBulkJob(ctx, fixture.orgA, queued.JobID, errors.New("private executor failure")); err != nil {
			t.Fatal(err)
		}
		var status, safeError string
		var attempts int
		var nextRetry time.Time
		if err := fixture.pool.QueryRow(fixture.ctx, `SELECT j.status,j.error_message,b.attempt_count,b.next_attempt_at FROM admin_jobs j JOIN admin_people_bulk_jobs b ON b.job_id=j.id WHERE j.id=$1`, queued.JobID).Scan(&status, &safeError, &attempts, &nextRetry); err != nil {
			t.Fatal(err)
		}
		if attempts != attempt || safeError != "executor_failure" {
			t.Fatalf("attempt %d state=%s/%s/%d", attempt, status, safeError, attempts)
		}
		if attempt < 5 {
			if status != "queued" || (!previousRetry.IsZero() && !nextRetry.After(previousRetry)) {
				t.Fatalf("attempt %d retry=%s previous=%s status=%s", attempt, nextRetry, previousRetry, status)
			}
			previousRetry = nextRetry
		} else if status != "failed" {
			t.Fatalf("terminal status=%s", status)
		}
	}
	var failedAudits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE target_type='bulk_job' AND target_id=$1 AND action='people.bulk_job_failed' AND outcome='failure'`, queued.JobID).Scan(&failedAudits); err != nil {
		t.Fatal(err)
	}
	if failedAudits != 1 {
		t.Fatalf("terminal failure audits=%d", failedAudits)
	}
}

func TestPolicyBulkJobDerivedCohortConvergesAndRestoreDefaultMovesInheritedOnly(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Derived custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Derived custom profile", customGroup)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyDeriveEntitlementCohort, CohortID: standard.ID, Name: "Two streams", Patch: entitlements.PolicyPatch{MaxStreams: intPointer(2)}}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-derived"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "derive-a", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	converged, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "derive-b", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if converged.JobID != queued.JobID || queued.TargetCohortID == uuid.Nil || queued.TargetCohortID == standard.ID {
		t.Fatalf("queued=%+v converged=%+v", queued, converged)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 {
		t.Fatalf("derived completion=%+v err=%v", completed, err)
	}
	var parent uuid.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT parent_id FROM entitlement_policy_cohort_revisions WHERE id=$1`, completed.TargetCohortID).Scan(&parent); err != nil || parent != standard.ID {
		t.Fatalf("derived parent=%s err=%v", parent, err)
	}
	fixture.assertProfileGroup(t, fixture.sharedAccountID, customProfile, customGroup)

	restoreSelection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	restoreCommand := PolicyCommand{Kind: PolicyRestoreDefaultEntitlement}
	restorePreview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, restoreSelection.Token, restoreCommand)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: restoreSelection.Token, ConfirmationToken: restorePreview.ConfirmationToken, IdempotencyKey: "restore-a", Command: restoreCommand})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, restore.JobID)
	if err != nil || restored.Succeeded != 1 || restored.TargetGroupID != int64(fixture.groupA) {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, fixture.groupA)
	fixture.assertProfileGroup(t, fixture.sharedAccountID, fixture.profileA, fixture.groupA)
	fixture.assertProfileGroup(t, fixture.sharedAccountID, customProfile, customGroup)
}

func TestPolicyBulkJobSkipsStaleProfileSnapshot(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "stale-profile", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET updated_at=updated_at+interval '1 second' WHERE organization_id=$1 AND user_id=$2`, fixture.orgA, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || len(completed.Skipped) != 1 || completed.Skipped[0].Reason != ReasonAuthorizationChanged {
		t.Fatalf("stale completion=%+v err=%v", completed, err)
	}
	fixture.assertAccountGroup(t, fixture.sharedAccountID, int(standard.AccessGroupID))
}

func TestPolicyBulkJobFailsBeforeMutationWhenActorRevoked(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "revoked-actor", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET legacy_role='user' WHERE id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, fixture.ownerMembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("revoked actor error=%v", err)
	}
	observed, err := fixture.service.GetBulkJob(fixture.ctx, fixture.orgA, queued.JobID)
	if err != nil || observed.Status != "failed" || observed.ProgressCurrent != 0 {
		t.Fatalf("revoked job=%+v err=%v", observed, err)
	}
}

func TestPolicyBulkJobCommitsSuccessfulTargetsAcrossPartialFailure(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	first, _ := fixture.addAccount(t, fixture.orgA, "partial-policy-a@example.test", "Partial A", "Partial A", int(standard.AccessGroupID), false)
	second, _ := fixture.addAccount(t, fixture.orgA, "partial-policy-b@example.test", "Partial B", "Partial B", int(standard.AccessGroupID), false)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=ANY($1::integer[]) AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, []int{first, second}, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "partial-policy-"})
	if err != nil || selection.Matched != 2 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "partial-target", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, second); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Succeeded != 1 || len(completed.Failed) != 1 || completed.Failed[0].AccountID != second || completed.Failed[0].Reason != ReasonNotFound {
		t.Fatalf("partial completion=%+v err=%v", completed, err)
	}
	fixture.assertAccountGroup(t, first, int(premium.AccessGroupID))
	fixture.assertAccountGroup(t, second, int(standard.AccessGroupID))
}

func TestPolicyBulkJobConcurrentIdenticalEnqueueReturnsOneJob(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	action := PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "concurrent-identical", Command: command}
	type outcome struct {
		result BulkResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			result, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, action)
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent enqueue errors = %v / %v", first.err, second.err)
	}
	if first.result.JobID == "" || first.result.JobID != second.result.JobID {
		t.Fatalf("concurrent jobs = %q / %q", first.result.JobID, second.result.JobID)
	}
	var jobs, receipts int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_people_bulk_jobs WHERE job_id=$1`, first.result.JobID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_people_bulk_job_receipts WHERE job_id=$1`, first.result.JobID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || receipts != 1 {
		t.Fatalf("durable jobs=%d receipts=%d", jobs, receipts)
	}
}

func TestPolicyBulkJobConcurrentOverlappingJobsHaveOneAssignmentEffect(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	assignAccountAndInheritedProfiles(t, fixture, fixture.sharedAccountID, int64(fixture.groupA), standard.AccessGroupID)
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-concurrent"})
	type queuedCommand struct {
		result BulkResult
		err    error
	}
	enqueue := func(key string, command PolicyCommand) queuedCommand {
		selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
		if err != nil {
			return queuedCommand{err: err}
		}
		preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
		if err != nil {
			return queuedCommand{err: err}
		}
		result, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: key, Command: command})
		return queuedCommand{result: result, err: err}
	}
	premiumJob := enqueue("concurrent-premium", PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID})
	restoreJob := enqueue("concurrent-default", PolicyCommand{Kind: PolicyRestoreDefaultEntitlement})
	if premiumJob.err != nil || restoreJob.err != nil {
		t.Fatalf("enqueue premium=%v restore=%v", premiumJob.err, restoreJob.err)
	}
	results := make(chan queuedCommand, 2)
	for _, job := range []BulkResult{premiumJob.result, restoreJob.result} {
		job := job
		go func() {
			result, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, job.JobID)
			results <- queuedCommand{result: result, err: err}
		}()
	}
	totalSucceeded, totalSkipped := 0, 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		totalSucceeded += result.result.Succeeded
		totalSkipped += len(result.result.Skipped)
	}
	if totalSucceeded != 1 || totalSkipped != 1 {
		t.Fatalf("concurrent succeeded=%d skipped=%d", totalSucceeded, totalSkipped)
	}
	var audits int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE request_id='policy-concurrent' AND action='people.bulk_policy_assigned'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("assignment audits=%d err=%v", audits, err)
	}
}

func TestPolicyBulkJobExecutesTenThousandTargetsExactlyOnce(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	premium := ensurePreviewCohort(t, fixture, store, "premium", 1)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO users (email,username,password_hash,role,enabled)
		SELECT 'policy-10k-'||n||'@example.test','Policy 10k '||n,'hash','user',true
		FROM generate_series(1,10000) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role,access_group_id)
		SELECT $1,id,'active','user',$2 FROM users WHERE email LIKE 'policy-10k-%@example.test'`, fixture.orgA, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "policy-10k-"})
	if err != nil || selection.Matched != 10000 || selection.Excluded != 0 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	command := PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: premium.ID}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1, PolicyRevision: 1, RequestID: "policy-10k"})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil || preview.Matched != 10000 {
		t.Fatalf("preview matched=%d err=%v", preview.Matched, err)
	}
	queued, err := fixture.service.EnqueuePolicyBulk(ctx, fixture.orgA, fixture.ownerID, PolicyBulkAction{SelectionToken: selection.Token, ConfirmationToken: preview.ConfirmationToken, IdempotencyKey: "policy-10k", Command: command})
	if err != nil || queued.ProgressTotal != 10000 {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
	completed, err := fixture.service.ProcessBulkJob(ctx, fixture.orgA, queued.JobID)
	if err != nil || completed.Status != "completed" || completed.ProgressCurrent != 10000 || completed.ProgressTotal != 10000 || completed.Succeeded != 10000 || len(completed.Skipped) != 0 || len(completed.Failed) != 0 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	var assignments int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE request_id='policy-10k' AND action='people.bulk_policy_assigned'`).Scan(&assignments); err != nil || assignments != 10000 {
		t.Fatalf("assignments=%d err=%v", assignments, err)
	}
}

func assignAccountAndInheritedProfiles(t *testing.T, fixture *peopleFixture, accountID int, oldGroupID, newGroupID int64) {
	t.Helper()
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, accountID, newGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2 AND access_group_id=$4`, fixture.orgA, accountID, newGroupID, oldGroupID); err != nil {
		t.Fatal(err)
	}
}

type peopleFixture struct {
	t                 *testing.T
	ctx               context.Context
	pool              *pgxpool.Pool
	service           *Service
	orgA              uuid.UUID
	orgB              uuid.UUID
	ownerID           int
	ownerMembershipID uuid.UUID
	sharedAccountID   int
	groupA            int
	groupB            int
	profileA          string
	profileB          string
}

func newPeopleFixture(t *testing.T) *peopleFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	pool := newPeopleDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write. Hand the authority over so these tests run against
	// the state the handoff produces.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}
	f := &peopleFixture{t: t, ctx: ctx, pool: pool, service: NewService(pool, "people-postgres-test-secret")}
	f.ownerID = f.insertUser(t, "owner@example.test", "Owner")
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&f.orgA); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='active', owner_account_id=$2 WHERE id=$1`, f.orgA, f.ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug,name,status,owner_account_id) VALUES ('people-org-b','People B','active',$1) RETURNING id`, f.ownerID).Scan(&f.orgB); err != nil {
		t.Fatal(err)
	}
	f.groupA = f.defaultGroup(t, f.orgA)
	f.groupB = f.addGroup(t, f.orgB, "B Default", true)
	f.ownerMembershipID = f.addMembership(t, f.orgA, f.ownerID, "admin")
	f.addMembership(t, f.orgB, f.ownerID, "admin")
	f.sharedAccountID = f.insertUser(t, "shared@example.test", "Shared Account")
	f.addMembership(t, f.orgA, f.sharedAccountID, "user")
	f.addMembership(t, f.orgB, f.sharedAccountID, "user")
	f.profileA = f.addProfile(t, f.sharedAccountID, f.orgA, "Alpha Profile", f.groupA)
	f.profileB = f.addProfile(t, f.sharedAccountID, f.orgB, "Foreign Profile", f.groupB)
	foreignID := f.insertUser(t, "shared-foreign@example.test", "Foreign Only")
	f.addMembership(t, f.orgB, foreignID, "user")
	f.addProfile(t, foreignID, f.orgB, "Foreign Shared", f.groupB)
	return f
}

func (f *peopleFixture) insertUser(t *testing.T, email, username string) int {
	t.Helper()
	var id int
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users (email,username,password_hash,role,enabled) VALUES ($1,$2,'hash','user',true) RETURNING id`, email, username).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *peopleFixture) addMembership(t *testing.T, org uuid.UUID, accountID int, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) SELECT $1,$2,'active',$3 WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL RETURNING id`, org, accountID, role).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *peopleFixture) defaultGroup(t *testing.T, org uuid.UUID) int {
	t.Helper()
	var id int
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *peopleFixture) addGroup(t *testing.T, org uuid.UUID, name string, isDefault bool) int {
	t.Helper()
	var id int
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO access_groups (organization_id,name,is_default) VALUES ($1,$2,$3) RETURNING id`, org, name, isDefault).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *peopleFixture) addProfile(t *testing.T, accountID int, org uuid.UUID, name string, group int) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO user_profiles (id,user_id,name,organization_id,access_group_id) VALUES ($1,$2,$3,$4,$5)`, id, accountID, name, org, group); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *peopleFixture) addAccount(t *testing.T, org uuid.UUID, email, username, profileName string, group int, admin bool) (int, string) {
	t.Helper()
	id := f.insertUser(t, email, username)
	role := "user"
	if admin {
		role = "admin"
	}
	f.addMembership(t, org, id, role)
	return id, f.addProfile(t, id, org, profileName, group)
}

func (f *peopleFixture) assertProfileGroup(t *testing.T, accountID int, profileID string, want int) {
	t.Helper()
	var got int
	if err := f.pool.QueryRow(f.ctx, `SELECT access_group_id FROM user_profiles WHERE user_id=$1 AND id=$2`, accountID, profileID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("profile group = %d, want %d", got, want)
	}
}

func (f *peopleFixture) assertAccountGroup(t *testing.T, accountID, want int) {
	t.Helper()
	var got int
	if err := f.pool.QueryRow(f.ctx, `SELECT access_group_id FROM organization_memberships WHERE account_id=$1`, accountID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("account group = %d, want %d", got, want)
	}
}

func (f *peopleFixture) assertMembershipRevision(t *testing.T, org uuid.UUID, accountID int, want int64) {
	t.Helper()
	var got int64
	if err := f.pool.QueryRow(f.ctx, `SELECT security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, org, accountID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("membership revision = %d, want %d", got, want)
	}
}

func (f *peopleFixture) assertAccountPolicyRevision(t *testing.T, accountID int, want int64) {
	t.Helper()
	var got int64
	if err := f.pool.QueryRow(f.ctx, `SELECT access_policy_revision FROM organization_memberships WHERE account_id=$1`, accountID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("account policy revision = %d, want %d", got, want)
	}
}

func (f *peopleFixture) assertMembershipStatusAndRevision(t *testing.T, org uuid.UUID, accountID int, wantStatus string, wantRevision int64) {
	t.Helper()
	var status string
	var revision int64
	if err := f.pool.QueryRow(f.ctx, `SELECT status,security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2`, org, accountID).Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || revision != wantRevision {
		t.Fatalf("membership = %s/%d, want %s/%d", status, revision, wantStatus, wantRevision)
	}
}

func newPeopleDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_adminpeople_" + hex.EncodeToString(random[:])
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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	return pool
}
