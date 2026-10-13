//go:build integration

package api

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type listRequestMetadataFixture struct{ requests.TMDBClient }

func (listRequestMetadataFixture) GetMediaDetail(_ context.Context, kind string, id int) (*tmdb.MediaDetail, error) {
	return &tmdb.MediaDetail{ID: id, Title: "Synthetic native list request"}, nil
}

func listNativeRequestFixture(t *testing.T) (*listCollectionDomain, listcollections.Subject, *requests.Service, context.Context) {
	t.Helper()
	d, subject, _ := listNativeDomainFixtureRole(t, "user")
	pool, ctx := d.source.pool, t.Context()
	if _, err := pool.Exec(ctx, `UPDATE request_settings SET requests_enabled=true,global_max_requests=1,global_auto_approval_enabled=false WHERE id=true`); err != nil {
		t.Fatal(err)
	}
	var organization uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT organization_id FROM user_profiles WHERE id=$1`, subject.ProfileID).Scan(&organization); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='active',owner_account_id=$2 WHERE id=$1`, organization, subject.UserID); err != nil {
		t.Fatal(err)
	}
	tenant, err := tenancy.NewResolver(tenancy.NewStore(pool)).ResolveProfile(ctx, subject.UserID, organization)
	if err != nil {
		t.Fatal(err)
	}
	ctx = apimw.SetProfileID(tenancy.WithContext(ctx, tenant), subject.ProfileID)
	ctx = access.SetScope(ctx, access.Scope{UserID: subject.UserID, ProfileID: subject.ProfileID, ProfileVerified: true})
	s := requests.NewService(requests.NewRepository(pool, d.managed.Cipher), listRequestMetadataFixture{}, nil)
	s.SetUserRepository(auth.NewUserRepository(pool))
	s.SetGroupPolicyProvider(access.NewTenantGroupStore(pool))
	s.SetTenantScopeResolver(tenancy.NewStore(pool))
	if err = s.EnableTenantRequestPolicy(); err != nil {
		t.Fatal(err)
	}

	// Activating the organization advances the managed profile incarnation.
	// Reconcile through the real enrollment service, as the plugin does.
	info, err := d.managed.GetInfo(ctx, subject.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.managed.ListProfiles(ctx, subject.InstallationID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Profiles {
		if p.ProfileID == subject.ProfileID {
			e := managedtracking.Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture", Active: true, Generation: info.Generation}
			e.ID = managedtracking.EnrollmentID(info.InstanceID, e)
			if err = d.managed.EnsureConnection(ctx, subject.InstallationID, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	return d, subject, s, ctx
}

func TestListFollowNativeRequestConcurrentReplayUsesOneRequestAndQuotaDebit(t *testing.T) {
	d, subject, s, ctx := listNativeRequestFixture(t)
	pool := d.source.pool
	var err error
	viewer := requests.Viewer{UserID: subject.UserID, ProfileID: subject.ProfileID}
	input := requests.CreateRequestInput{MediaType: requests.MediaTypeMovie, TMDBID: 42, Title: "Source title"}
	// Two concurrent dispatch attempts transport the same actor/identity key.
	// Both may pass the initial lookup before the native quota lock admits one.
	key := "synthetic-two-bindings-replay-key"
	var wg sync.WaitGroup
	ids := make([]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			r, e := s.CreateListRequest(ctx, viewer, input, key)
			errs[i] = e
			if r != nil {
				ids[i] = r.ID
			}
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("native concurrent replay: ids=%v errors=%v", ids, errs)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM media_requests WHERE requested_by_user_id=$1 AND tmdb_id=42`, subject.UserID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("native request duplicated: count=%d err=%v", count, err)
	}
	input.TMDBID = 43
	if _, err = s.CreateListRequest(ctx, viewer, input, "synthetic-another-title-replay-key"); err == nil {
		t.Fatal("native quota was bypassed")
	}
	if _, err = s.CreateListRequest(ctx, viewer, requests.CreateRequestInput{MediaType: requests.MediaTypeMovie, TMDBID: 42, Title: "Changed source label"}, key); err != nil {
		t.Fatal("native receipt replay charged quota again", err)
	}
}

func TestListFollowDeliveryTwoBindingsUseRealNativeRequestsAndQuota(t *testing.T) {
	d, subject, native, ctx := listNativeRequestFixture(t)
	d.source.prepare = func(context.Context, managedtracking.Profile) (context.Context, error) { return ctx, nil }
	s := &listcollections.Service{Pool: d.source.pool, Cipher: d.managed.Cipher, Domain: d}
	s.Requests = &listCollectionRequests{domain: d, movies: native, books: bookrequests.NewService(d.source.pool, nil)}
	tx, authorityErr := d.source.pool.Begin(ctx)
	if authorityErr != nil {
		t.Fatal(authorityErr)
	}
	authorityErr = d.AuthorizeTx(ctx, tx, subject)
	tx.Rollback(ctx)
	if authorityErr != nil {
		t.Fatal("fresh fixture authority", authorityErr)
	}
	e := listcollections.Entry{Kind: "movie", Title: "Synthetic", ExternalIDs: map[string]string{"tmdb": "42"}}
	seedListDeliveryBinding(t, s, subject, e)
	e.Title = "Another list label"
	seedListDeliveryBinding(t, s, subject, e)
	got, err := s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Delivered != 2 {
		t.Fatalf("native two-binding delivery: %+v %v", got, err)
	}
	var count int
	if err = s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM media_requests WHERE requested_by_user_id=$1`, subject.UserID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("native duplicates: %d %v", count, err)
	}
	e.ExternalIDs["tmdb"] = "43"
	seedListDeliveryBinding(t, s, subject, e)
	got, err = s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Blocked != 1 {
		t.Fatalf("native quota not reported: %+v %v", got, err)
	}
}

func TestListFollowDeliveryBooksKeepFormatEditionAndNativeQuota(t *testing.T) {
	d, subject, native, ctx := listNativeRequestFixture(t)
	d.source.prepare = func(context.Context, managedtracking.Profile) (context.Context, error) { return ctx, nil }
	if _, err := d.source.pool.Exec(ctx, `INSERT INTO server_settings(key,value)VALUES($1,'true') ON CONFLICT(key)DO UPDATE SET value=EXCLUDED.value`, config.EbookRequestsEnabledSettingKey); err != nil {
		t.Fatal(err)
	}
	s := &listcollections.Service{Pool: d.source.pool, Cipher: d.managed.Cipher, Domain: d}
	s.Requests = &listCollectionRequests{domain: d, movies: native, books: bookrequests.NewService(d.source.pool, nil)}
	for _, kind := range []string{"ebook", "audiobook"} {
		e := listcollections.Entry{Kind: kind, Title: "Synthetic edition", ExternalIDs: map[string]string{"isbn": "0306406152"}}
		seedListDeliveryBinding(t, s, subject, e)
		e.Title = "Renamed source"
		e.ExternalIDs["isbn"] = "9780306406157"
		seedListDeliveryBinding(t, s, subject, e)
	}
	got, err := s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Delivered != 4 {
		t.Fatalf("book delivery: %+v %v", got, err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_book_requests WHERE requested_by_user_id=$1 AND isbn='9780306406157'`, subject.UserID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("book edition/format collapsed or duplicated: %d %v", count, err)
	}
	for _, kind := range []string{"ebook", "audiobook"} {
		seedListDeliveryBinding(t, s, subject, listcollections.Entry{Kind: kind, Title: "Other edition", ExternalIDs: map[string]string{"isbn": "9780131103627"}})
	}
	got, err = s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Blocked != 2 {
		t.Fatalf("native book quota bypassed: %+v %v", got, err)
	}
	seedListDeliveryBinding(t, s, subject, listcollections.Entry{Kind: "ebook", Title: "ASIN only", ExternalIDs: map[string]string{"asin": "B000000001"}})
	got, err = s.DeliverRequests(t.Context(), 10)
	if err != nil || got.Blocked != 1 {
		t.Fatalf("ASIN-only ebook downgraded: %+v %v", got, err)
	}
}
