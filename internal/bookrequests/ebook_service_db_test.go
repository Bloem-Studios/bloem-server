package bookrequests

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setEbookOptIn(t *testing.T, pool *pgxpool.Pool, value string) {
	t.Helper()
	bookSQL(t, pool, `INSERT INTO server_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, config.EbookRequestsEnabledSettingKey, value)
}

func TestInternalEbookAdmissionGateDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	in := EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}
	if _, _, err := s.CreateEbook(t.Context(), v, in, uuid.NewString()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("missing opt-in admitted ebook: %v", err)
	}
	for _, value := range []string{"false", "enabled", "TRUE", "1", "enc:v1:true"} {
		setEbookOptIn(t, pool, value)
		if _, _, err := s.CreateEbook(t.Context(), v, in, uuid.NewString()); !errors.Is(err, ErrDisabled) {
			t.Errorf("unvalidated opt-in %q admitted ebook: %v", value, err)
		}
	}
	setEbookOptIn(t, pool, "true")
	hosted, _ := hostedBookViewer(t, pool, v.UserID)
	bookSQL(t, pool, `UPDATE users SET role='admin' WHERE id=$1`, v.UserID)
	hosted.Admin = true
	if _, _, err := s.CreateEbook(t.Context(), hosted, in, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("platform-admin account bypassed hosted restriction: %v", err)
	}
	if r := submit(t, s, hosted, CreateInput{Title: "Hosted audiobook", Author: "Author"}); r.MediaType != "audiobook" {
		t.Fatalf("hosted audiobook behavior changed: %+v", r)
	}
	if _, _, err := s.CreateEbook(t.Context(), v, in, uuid.NewString()); err != nil {
		t.Fatalf("opted-in default organization denied: %v", err)
	}
	bookSQL(t, pool, `UPDATE access_groups SET requests_allowed=false WHERE organization_id=$1`, v.OrganizationID)
	if _, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Other edition", ISBN: "9780131103627"}, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ebook bypassed profile-group request policy: %v", err)
	}
	bookSQL(t, pool, `UPDATE access_groups SET requests_allowed=true WHERE organization_id=$1`, v.OrganizationID)
	bookSQL(t, pool, `UPDATE request_settings SET requests_enabled=false`)
	if _, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Other edition", ISBN: "9780131103627"}, uuid.NewString()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("ebook bypassed global request gate: %v", err)
	}
}

func TestInternalEbookIdentityRetryAndIsolationDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	in := EbookCreateInput{Title: " Edition ", Author: " Author ", ISBN: "0-306-40615-2", Note: " note "}
	key := "ebook-normalized-retry-key"
	r, created, err := s.CreateEbook(t.Context(), v, in, key)
	if err != nil || !created || r.MediaType != "book" || r.ISBN != "9780306406157" || r.Title != "Edition" || r.ASIN != "" || r.Status != "pending" {
		t.Fatalf("canonical creation: %+v %v", r, err)
	}
	in.ISBN = "9780306406157"
	replay, created, err := s.CreateEbook(t.Context(), v, in, key)
	if err != nil || created || replay.ID != r.ID {
		t.Fatalf("ISBN retry: %+v created=%v %v", replay, created, err)
	}
	changed := in
	changed.Title = "Changed edition"
	if _, _, err := s.CreateEbook(t.Context(), v, changed, key); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry accepted: %v", err)
	}
	if _, _, err := s.CreateEbook(t.Context(), v, in, uuid.NewString()); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("canonical edition not deduped: %v", err)
	}
	for _, isbn := range []string{"", "9780306406158", "B012345678"} {
		if _, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", Author: "Author", ISBN: isbn}, uuid.NewString()); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid/missing ebook ISBN %q accepted: %v", isbn, err)
		}
	}
	audio := submit(t, s, v, CreateInput{Title: "Edition", ISBN: r.ISBN})
	if _, _, err := s.Create(t.Context(), v, CreateInput{Title: "Edition", Author: "Author", ISBN: r.ISBN, Note: "note"}, key); !errors.Is(err, ErrConflict) {
		t.Errorf("audiobook retry reused ebook key: %v", err)
	}
	if _, err := s.GetEbook(t.Context(), v, audio.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ebook read exposed audiobook: %v", err)
	}
	sibling := v
	sibling.ProfileID = "sibling"
	if _, err := s.GetEbook(t.Context(), sibling, r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("sibling read ebook: %v", err)
	}
	wrong := v
	wrong.OrganizationID, wrong.Admin = uuid.New(), true
	if _, err := s.GetEbook(t.Context(), wrong, r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign admin read ebook: %v", err)
	}
	bookSQL(t, pool, `UPDATE users SET enabled=false WHERE id=$1`, v.UserID)
	if _, err := s.GetEbook(t.Context(), v, r.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("disabled account retained ebook access: %v", err)
	}
}

func otherEbookViewer(t *testing.T, pool *pgxpool.Pool, v Viewer) Viewer {
	t.Helper()
	var account int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('other-ebook','other-ebook@example.test','x','user') RETURNING id`).Scan(&account); err != nil {
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
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','user')`, v.OrganizationID, account); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO user_profiles(user_id,id,name,organization_id,access_group_id) SELECT $1,'viewer','Other viewer',$2,id FROM access_groups WHERE organization_id=$2 AND is_default`, account, v.OrganizationID)
	v.UserID = account
	return v
}

func TestInternalEbookConcurrentISBNDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	other := otherEbookViewer(t, pool, v)
	start, results := make(chan struct{}), make(chan error, 2)
	var wg sync.WaitGroup
	for i, viewer := range []Viewer{v, other} {
		wg.Go(func() {
			<-start
			isbn := []string{"0306406152", "9780306406157"}[i]
			_, _, err := s.CreateEbook(t.Context(), viewer, EbookCreateInput{Title: "Edition", ISBN: isbn}, uuid.NewString())
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	created, duplicate := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrDuplicate):
			duplicate++
		default:
			t.Fatal(err)
		}
	}
	if created != 1 || duplicate != 1 {
		t.Fatalf("concurrent ISBN dedupe: created=%d duplicate=%d", created, duplicate)
	}
}

func TestInternalEbookQuotaDoesNotChangeAudiobookOrTMDBDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	bookSQL(t, pool, `UPDATE request_settings SET global_max_requests=1`)
	bookSQL(t, pool, `INSERT INTO media_requests(id,media_type,tmdb_id,title,status,requested_by_user_id,requested_by_profile_id) VALUES('untouched-tmdb','movie',101,'Movie','pending',$1,$2)`, v.UserID, v.ProfileID)
	submit(t, s, v, CreateInput{Title: "Audio edition", ISBN: "9780306406157"})
	if _, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}, uuid.NewString()); err != nil {
		t.Fatalf("audiobook/TMDB counter consumed ebook quota: %v", err)
	}
	if _, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Another edition", ISBN: "9780131103627"}, uuid.NewString()); !errors.Is(err, ErrQuota) {
		t.Fatalf("ebook quota bypassed: %v", err)
	}
	cap, err := s.Capability(t.Context(), v)
	if err != nil || cap.Used != 1 || cap.Remaining != 0 {
		t.Fatalf("ebook changed audiobook count: %+v %v", cap, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM media_requests WHERE id='untouched-tmdb' AND status='pending' AND tmdb_id=101`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("TMDB request changed: count=%d %v", count, err)
	}
}

func TestInternalEbookLifecycleSurvivesOptOutDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	r, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Other edition", ISBN: "9780131103627"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActEbook(t.Context(), v, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("requester approved ebook: %v", err)
	}
	admin := v
	admin.Admin = true
	approved, err := s.ActEbook(t.Context(), admin, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision})
	if err != nil || approved.Status != "approved" || approved.Fulfilment != "awaiting_fulfilment" || approved.Revision != r.Revision+1 {
		t.Fatalf("ebook approval: %+v %v", approved, err)
	}
	setEbookOptIn(t, pool, "false")
	bookSQL(t, pool, `UPDATE request_settings SET requests_enabled=false`)
	bookSQL(t, pool, `UPDATE access_groups SET requests_allowed=false WHERE organization_id=$1`, v.OrganizationID)
	if _, err := s.GetEbook(t.Context(), v, r.ID); err != nil {
		t.Fatalf("opt-out hid existing ebook: %v", err)
	}
	if _, err := s.ActEbook(t.Context(), admin, second.ID, "approve", ActionInput{ExpectedRevision: second.Revision}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("opt-out permitted approval: %v", err)
	}
	if _, err := s.ActEbook(t.Context(), v, r.ID, "cancel", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ebook action accepted: %v", err)
	}
	cancelled, err := s.ActEbook(t.Context(), v, r.ID, "cancel", ActionInput{ExpectedRevision: approved.Revision})
	if err != nil || cancelled.Outcome != "cancelled" {
		t.Fatalf("opt-out prevented cancellation: %+v %v", cancelled, err)
	}
	denied, err := s.ActEbook(t.Context(), admin, second.ID, "decline", ActionInput{ExpectedRevision: second.Revision})
	if err != nil || denied.Outcome != "declined" {
		t.Fatalf("opt-out prevented denial: %+v %v", denied, err)
	}
	bookSQL(t, pool, `UPDATE users SET enabled=false WHERE id=$1`, v.UserID)
	if _, err := s.GetEbook(t.Context(), admin, second.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disabled admin retained authority: %v", err)
	}
}

func TestLegacyAudiobookDigestReplayDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	id, key := uuid.NewString(), "legacy-audiobook-retry-key"
	// Literal pre-extension normalized payload: media type must not be added.
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"title":"Legacy audiobook","author":"Author"}`)))
	bookSQL(t, pool, `INSERT INTO bloem_book_requests(id,organization_id,identity_key,title,author,status,requested_by_user_id,requested_by_profile_id,client_key,request_digest) VALUES($1,$2,'legacy-fixture','Legacy audiobook','Author','pending',$3,$4,$5,$6)`, id, v.OrganizationID, v.UserID, v.ProfileID, key, digest)
	r, created, err := s.Create(t.Context(), v, CreateInput{Title: " Legacy audiobook ", Author: "Author"}, key)
	if err != nil || created || r.ID != id || r.MediaType != "audiobook" {
		t.Fatalf("legacy audiobook retry changed: %+v created=%v %v", r, created, err)
	}
}

func TestInternalEbookRevokedMembershipDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	r, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}, uuid.NewString())
	if err != nil {
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
	if _, err = tx.Exec(t.Context(), `UPDATE organization_memberships SET status='suspended' WHERE account_id=$1 AND organization_id=$2`, v.UserID, v.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, admin := range []bool{false, true} {
		v.Admin = admin
		if _, err := s.GetEbook(t.Context(), v, r.ID); !errors.Is(err, ErrForbidden) {
			t.Errorf("revoked membership read ebook (admin=%v): %v", admin, err)
		}
		if _, err := s.ActEbook(t.Context(), v, r.ID, "cancel", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrForbidden) {
			t.Errorf("revoked membership cancelled ebook (admin=%v): %v", admin, err)
		}
	}
}

type ebookNoticeSpy struct{ events []string }

func (n *ebookNoticeSpy) Lifecycle(_ context.Context, r Request, event string) {
	n.events = append(n.events, r.MediaType+":"+event)
}
func (*ebookNoticeSpy) Fulfilled(context.Context, Request) error { return nil }

func TestInternalEbookDoesNotUseAudiobookNotificationsDatabase(t *testing.T) {
	s, v, pool := databaseFixture(t)
	setEbookOptIn(t, pool, "true")
	bookSQL(t, pool, `UPDATE request_settings SET global_auto_approval_enabled=true`)
	n := &ebookNoticeSpy{}
	s.notifier = n
	r, _, err := s.CreateEbook(t.Context(), v, EbookCreateInput{Title: "Edition", ISBN: "9780306406157"}, uuid.NewString())
	if err != nil || r.Status != "approved" {
		t.Fatalf("auto-approval: %+v %v", r, err)
	}
	if _, err := s.ActEbook(t.Context(), v, r.ID, "cancel", ActionInput{ExpectedRevision: r.Revision}); err != nil {
		t.Fatal(err)
	}
	submit(t, s, v, CreateInput{Title: "Audio edition", ISBN: "9780306406157"})
	if len(n.events) != 2 || n.events[0] != "audiobook:submitted" || n.events[1] != "audiobook:auto_approved" {
		t.Fatalf("ebook used audiobook notices or audio behavior changed: %v", n.events)
	}
}
