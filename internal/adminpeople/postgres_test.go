package adminpeople

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
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
	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
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

func TestServiceCleanupRemovesOnlyExpiredSelections(t *testing.T) {
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
	expired, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	expiredID, _ := fixture.service.parseSelectionReference(expired.Token)
	removed, err := fixture.service.CleanupExpiredSelections(fixture.ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup = %d, %v", removed, err)
	}
	var activeExists, expiredExists bool
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT EXISTS(SELECT 1 FROM admin_people_selections WHERE id=$1),EXISTS(SELECT 1 FROM admin_people_selections WHERE id=$2)`, activeID, expiredID).Scan(&activeExists, &expiredExists); err != nil {
		t.Fatal(err)
	}
	if !activeExists || expiredExists {
		t.Fatalf("selection existence active=%t expired=%t", activeExists, expiredExists)
	}
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
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := fixture.service.ProcessBulkJob(cancelled, fixture.orgA, queued.JobID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled process error = %v", err)
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
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_people_bulk_jobs WHERE selection_id=(SELECT id FROM admin_people_selections LIMIT 1)`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("durable jobs=%d", jobs)
	}
}

func TestServiceBulkSkipsAuthorizationStateChangedAfterSelection(t *testing.T) {
	fixture := newPeopleFixture(t)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityPlatformAdmin, RequestID: "concurrent-change"})
	queued, err := fixture.service.EnqueueBulk(ctx, fixture.orgA, fixture.ownerID, BulkAction{SelectionToken: selection.Token, Kind: BulkSuspendMemberships})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET security_revision=security_revision+1 WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, fixture.sharedAccountID); err != nil {
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
	if result.Succeeded != 2 { // shared and regular; the owner is protected.
		t.Fatalf("succeeded = %d, result=%+v", result.Succeeded, result)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].AccountID != fixture.ownerID || result.Skipped[0].Reason != ReasonProtectedOwner {
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

type peopleFixture struct {
	t               *testing.T
	ctx             context.Context
	pool            *pgxpool.Pool
	service         *Service
	orgA            uuid.UUID
	orgB            uuid.UUID
	ownerID         int
	sharedAccountID int
	groupA          int
	groupB          int
	profileA        string
	profileB        string
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
	f.addMembership(t, f.orgA, f.ownerID, "admin")
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

func (f *peopleFixture) addMembership(t *testing.T, org uuid.UUID, accountID int, role string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role) VALUES ($1,$2,'active',$3)`, org, accountID, role); err != nil {
		t.Fatal(err)
	}
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
	if err := f.pool.QueryRow(f.ctx, `SELECT access_policy_revision FROM users WHERE id=$1`, accountID).Scan(&got); err != nil {
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
	name := "vondel_adminpeople_" + hex.EncodeToString(random[:])
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
