package bookrequests

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func databaseFixture(t *testing.T) (*Service, Viewer, *pgxpool.Pool) {
	return databaseFixtureWithMigrations(t, migrations.BloemFS)
}

func databaseFixtureWithMigrations(t *testing.T, migrationFS fs.FS) (*Service, Viewer, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL required")
		}
		t.Skip("SILO_TEST_DATABASE_URL not set")
	}
	name := "bloem_book_requests_test_" + fmt.Sprintf("%x", uuid.New())[:12]
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, name)
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	if err = database.RunMigrations(t.Context(), pool, migrationFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err = tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	var org uuid.UUID
	if err = pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var user int
	if err = pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('requester','requester@example.test','x','user') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = tenancy.MarkMembershipPolicyWriter(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user') ON CONFLICT DO NOTHING`, org, user); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `INSERT INTO user_profiles(user_id,id,name,organization_id,access_group_id) SELECT $1,v.id,v.name,$2,g.id FROM access_groups g CROSS JOIN (VALUES ('viewer','Viewer'),('sibling','Sibling')) v(id,name) WHERE g.organization_id=$2 AND g.is_default`, user, org); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE request_settings SET requests_enabled=true,global_max_requests=10,global_auto_approval_enabled=false WHERE id=true`); err != nil {
		t.Fatal(err)
	}
	return NewService(pool, nil), Viewer{UserID: user, ProfileID: "viewer", OrganizationID: org}, pool
}
func submit(t *testing.T, s *Service, v Viewer, in CreateInput) Request {
	t.Helper()
	r, _, e := s.Create(t.Context(), v, in, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestBookRequestPolicyIsolationIdempotencyAndManualLifecycleDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	r, created, err := s.Create(t.Context(), v, CreateInput{Title: "Book", ISBN: "0306406152"}, "unique-key-for-retry-1")
	if err != nil || !created || r.Status != "pending" {
		t.Fatalf("create: %+v %v", r, err)
	}
	replay, created, err := s.Create(t.Context(), v, CreateInput{Title: "Book", ISBN: "9780306406157"}, "unique-key-for-retry-1")
	if err != nil || created || replay.ID != r.ID {
		t.Fatalf("retry: %+v %v", replay, err)
	}
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Changed", ISBN: r.ISBN}, "unique-key-for-retry-1"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Book", ISBN: r.ISBN}, uuid.NewString()); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	sibling := v
	sibling.ProfileID = "sibling"
	if _, err = s.Get(t.Context(), sibling, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("sibling saw request", err)
	}
	wrong := v
	wrong.OrganizationID = uuid.New()
	wrong.Admin = true
	if _, err = s.Get(t.Context(), wrong, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign admin saw request", err)
	}
	if _, err = s.Act(t.Context(), v, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatal("requester approved request", err)
	}
	admin := v
	admin.Admin = true
	approved, err := s.Act(t.Context(), admin, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision})
	if err != nil || approved.Status != "approved" || approved.Fulfilment != "awaiting_fulfilment" {
		t.Fatalf("approve: %+v %v", approved, err)
	}
	if n, err := s.Reconcile(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("unconfigured backend reconciliation: %d %v", n, err)
	}
	still, err := s.Get(t.Context(), v, r.ID)
	if err != nil || still.Status != "approved" {
		t.Fatalf("should wait: %+v %v", still, err)
	}
	if _, err = s.Act(t.Context(), admin, r.ID, "decline", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale action allowed", err)
	}
	done, err := s.Act(t.Context(), admin, r.ID, "fulfil", ActionInput{ExpectedRevision: approved.Revision})
	if err != nil || done.Status != "completed" || done.Fulfilment != "manual" {
		t.Fatalf("manual: %+v %v", done, err)
	}
	pending := submit(t, s, v, CreateInput{Title: "Declined book", Author: "Author"})
	if _, err = s.Act(t.Context(), admin, pending.ID, "decline", ActionInput{ExpectedRevision: pending.Revision, Reason: "Unavailable edition"}); err != nil {
		t.Fatal(err)
	}
	cap, err := s.Capability(t.Context(), v)
	if err != nil || cap.Used != 1 {
		t.Fatalf("decline must refund quota: %+v %v", cap, err)
	}
	if _, err = pool.Exec(t.Context(), `UPDATE access_groups SET requests_allowed=false WHERE id=(SELECT access_group_id FROM user_profiles WHERE user_id=$1 AND id=$2)`, v.UserID, v.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Denied", Author: "Author"}, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatal("group gate ignored", err)
	}
}

func TestBookRequestConcurrentIdempotencyAndCrossAccountDedupeDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	start := make(chan struct{})
	type result struct {
		r       Request
		created bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			r, created, err := s.Create(t.Context(), v, CreateInput{Title: "One filing", Author: "Author"}, "concurrent-retry-key-1")
			results <- result{r, created, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	created, id := 0, ""
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.created {
			created++
		}
		if id != "" && got.r.ID != id {
			t.Fatalf("key replay returned different rows: %s %s", id, got.r.ID)
		}
		id = got.r.ID
	}
	if created != 1 {
		t.Fatalf("created=%d want=1", created)
	}
	var other int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('other','other@example.test','x','user') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err = tenancy.MarkMembershipPolicyWriter(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user')`, v.OrganizationID, other); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO user_profiles(user_id,id,name,organization_id,access_group_id) SELECT $1,'viewer','Other viewer',$2,id FROM access_groups WHERE organization_id=$2 AND is_default`, other, v.OrganizationID)
	otherViewer := v
	otherViewer.UserID = other
	start = make(chan struct{})
	results = make(chan result, 2)
	for _, viewer := range []Viewer{v, otherViewer} {
		wg.Go(func() {
			<-start
			r, created, err := s.Create(t.Context(), viewer, CreateInput{Title: "Edition", ASIN: "B012345678"}, uuid.NewString())
			results <- result{r, created, err}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, duplicate := 0, 0
	for got := range results {
		if got.err == nil {
			success++
		} else if errors.Is(got.err, ErrDuplicate) {
			duplicate++
		} else {
			t.Fatal(got.err)
		}
	}
	if success != 1 || duplicate != 1 {
		t.Fatalf("success=%d duplicate=%d", success, duplicate)
	}
}

func TestBookRequestQuotaAndDedupeConcurrencyDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	if _, err := pool.Exec(t.Context(), `UPDATE request_settings SET global_max_requests=1,global_auto_approval_enabled=true`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			<-start
			_, _, err := s.Create(t.Context(), v, CreateInput{Title: fmt.Sprint(i), Author: "Author"}, uuid.NewString())
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, quota := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrQuota) {
			quota++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || quota != 1 {
		t.Fatalf("success=%d quota=%d", success, quota)
	}
	rows, err := s.List(t.Context(), v, 100, 0)
	if err != nil || len(rows) != 1 || rows[0].Status != "approved" {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	cancelled, err := s.Act(t.Context(), v, rows[0].ID, "cancel", ActionInput{ExpectedRevision: rows[0].Revision})
	if err != nil || cancelled.Outcome != "cancelled" {
		t.Fatal(err)
	}
	cap, err := s.Capability(t.Context(), v)
	if err != nil || cap.Used != 1 || cap.Remaining != 0 {
		t.Fatalf("withdraw must consume quota: %+v %v", cap, err)
	}
}

func TestBookRequestOnlyAccessibleAudiobookCompletesDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	v, _ = hostedBookViewer(t, pool, v.UserID)
	r := submit(t, s, v, CreateInput{Title: "Stable book", ISBN: "9780306406157"})
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var platform uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM resource_owners WHERE kind='platform'`).Scan(&platform); err != nil {
		t.Fatal(err)
	}
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(id,name,type,owner_id) VALUES(919001,'Fixture audio','audiobooks',$1) RETURNING id`, platform).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO media_items(content_id,type,title) VALUES('book-fixture','ebook','Stable book')`)
	exec(`INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('book-fixture','ebook','isbn','0-306-40615-2')`)
	exec(`INSERT INTO media_files(id,media_folder_id,file_path,content_id) VALUES(919001,$1,'/fixture/book.m4b','book-fixture')`, folder)
	if n, e := s.Reconcile(t.Context(), 100); e != nil || n != 0 {
		t.Fatalf("foreign ebook completed: %d %v", n, e)
	}
	exec(`UPDATE media_items SET type='audiobook' WHERE content_id='book-fixture'; UPDATE media_item_provider_ids SET item_type='audiobook' WHERE content_id='book-fixture'`)
	if n, e := s.Reconcile(t.Context(), 100); e != nil || n != 0 {
		t.Fatalf("unentitled audio completed: %d %v", n, e)
	}
	exec(`INSERT INTO organization_entitlements(organization_id,media_folder_id,root_owner_id,root_kind,entitlement_kind,status,granted_by_service) VALUES($1,$2,$3,'media_folder','library_access','active','book-request-test')`, v.OrganizationID, folder, platform)
	if n, e := s.Reconcile(t.Context(), 100); e != nil || n != 1 {
		t.Fatalf("accessible audio: %d %v", n, e)
	}
	done, e := s.Get(t.Context(), v, r.ID)
	if e != nil || done.Status != "completed" || done.LibraryContentID != "book-fixture" || done.Fulfilment != "library" {
		t.Fatalf("completed: %+v %v", done, e)
	}
	if n, e := s.Reconcile(t.Context(), 100); e != nil || n != 0 {
		t.Fatalf("completion repeated: %d %v", n, e)
	}
}

func bookSQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func hostedBookViewer(t *testing.T, pool *pgxpool.Pool, account int) (Viewer, int64) {
	t.Helper()
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('hosted','Hosted','active',$1) RETURNING id`, account).Scan(&org); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err = tenancy.MarkMembershipPolicyWriter(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user')`, org, account); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var group int64
	if err = pool.QueryRow(t.Context(), `INSERT INTO access_groups(name,organization_id,is_default,requests_allowed) VALUES('Hosted requests',$1,true,true) RETURNING id`, org).Scan(&group); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO user_profiles(user_id,id,name,organization_id,access_group_id) VALUES($1,'hosted-viewer','Hosted viewer',$2,$3)`, account, org, group)
	return Viewer{UserID: account, ProfileID: "hosted-viewer", OrganizationID: org}, group
}

func TestBookRequestHostedProfilePolicyAndIndependentLaneDatabase(t *testing.T) {
	s, defaultViewer, pool := databaseFixture(t)
	v, group := hostedBookViewer(t, pool, defaultViewer.UserID)
	bookSQL(t, pool, `UPDATE access_groups SET requests_allowed=false WHERE organization_id=$1`, defaultViewer.OrganizationID)
	bookSQL(t, pool, `INSERT INTO request_group_limits(group_id,limit_mode,max_requests,window_days,approval_mode) VALUES($1,'custom',2,3,'auto')`, group)
	bookSQL(t, pool, `INSERT INTO media_requests(id,media_type,tmdb_id,title,status,requested_by_user_id,requested_by_profile_id) VALUES('silo-request','movie',101,'Movie','pending',$1,$2)`, v.UserID, v.ProfileID)
	cap, err := s.Capability(t.Context(), v)
	if err != nil || !cap.CanRequest || !cap.AutoApprove || cap.Limit != 2 || cap.WindowDays != 3 || cap.Used != 0 {
		t.Fatalf("hosted group policy: %+v %v", cap, err)
	}
	r := submit(t, s, v, CreateInput{Title: "Hosted audiobook", Author: "Author"})
	if r.Status != "approved" || r.OrganizationID != v.OrganizationID || r.RequestedByProfileID != v.ProfileID {
		t.Fatalf("hosted filing: %+v", r)
	}
	if _, _, err = s.Create(t.Context(), defaultViewer, CreateInput{Title: "Default denied", Author: "Author"}, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("default group bypass: %v", err)
	}
	defaultAdmin := defaultViewer
	defaultAdmin.Admin = true
	rows, err := s.List(t.Context(), defaultAdmin, 100, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross organization admin queue: %+v %v", rows, err)
	}
	bookSQL(t, pool, `INSERT INTO request_user_limits(user_id,limit_mode,max_requests,window_days,approval_mode) VALUES($1,'custom',1,5,'manual')`, v.UserID)
	cap, err = s.Capability(t.Context(), v)
	if err != nil || cap.AutoApprove || cap.Limit != 1 || cap.WindowDays != 5 || cap.Used != 1 || cap.Remaining != 0 {
		t.Fatalf("account override: %+v %v", cap, err)
	}
	bookSQL(t, pool, `UPDATE request_user_limits SET limit_mode='unlimited',approval_mode='blocked' WHERE user_id=$1`, v.UserID)
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Blocked", Author: "Author"}, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("blocked account policy bypassed: %v", err)
	}
	bookSQL(t, pool, `UPDATE request_user_limits SET limit_mode='inherit',approval_mode='inherit' WHERE user_id=$1`, v.UserID)
	bookSQL(t, pool, `UPDATE organizations SET status='suspended' WHERE id=$1`, v.OrganizationID)
	if _, _, err = s.Create(t.Context(), v, CreateInput{Title: "Suspended", Author: "Author"}, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("suspended hosted organization filed: %v", err)
	}
}

func TestBookRequestStableIdentifiersMustAgreeDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	r := submit(t, s, v, CreateInput{Title: "Stable edition", ISBN: "9780306406157", ASIN: "B012345678"})
	titleOnly := submit(t, s, v, CreateInput{Title: "Stable edition", Author: "Same author"})
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM resource_owners WHERE organization_id=$1`, v.OrganizationID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO media_folders(id,name,type,owner_id) VALUES(919002,'Owned audio','audiobooks',$1)`, owner)
	bookSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES('isbn-edition','audiobook','Stable edition'),('asin-edition','audiobook','Stable edition')`)
	bookSQL(t, pool, `INSERT INTO media_files(id,media_folder_id,file_path,content_id) VALUES(919002,919002,'/fixture/isbn.m4b','isbn-edition'),(919003,919002,'/fixture/asin.m4b','asin-edition')`)
	bookSQL(t, pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('isbn-edition','audiobook','isbn','9780306406157'),('asin-edition','audiobook','asin','B012345678')`)
	if n, err := s.Reconcile(t.Context(), 100); err != nil || n != 0 {
		t.Fatalf("conflicting identifiers completed a request: %d %v", n, err)
	}
	bookSQL(t, pool, `DELETE FROM media_item_provider_ids WHERE content_id='asin-edition'`)
	bookSQL(t, pool, `INSERT INTO media_item_provider_ids(content_id,item_type,provider,provider_id) VALUES('isbn-edition','audiobook','asin','B012345678')`)
	if n, err := s.Reconcile(t.Context(), 100); err != nil || n != 1 {
		t.Fatalf("single matching edition: %d %v", n, err)
	}
	done, err := s.Get(t.Context(), v, r.ID)
	if err != nil || done.LibraryContentID != "isbn-edition" {
		t.Fatalf("matching item: %+v %v", done, err)
	}
	waiting, err := s.Get(t.Context(), v, titleOnly.ID)
	if err != nil || waiting.Status == "completed" {
		t.Fatalf("title-only request completed: %+v %v", waiting, err)
	}
}
