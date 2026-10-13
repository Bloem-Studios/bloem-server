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
	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type listRequestMetadataFixture struct{ requests.TMDBClient }

func (listRequestMetadataFixture) GetMediaDetail(_ context.Context, kind string, id int) (*tmdb.MediaDetail, error) {
	return &tmdb.MediaDetail{ID: id, Title: "Synthetic native list request"}, nil
}

func TestListFollowNativeRequestConcurrentReplayUsesOneRequestAndQuotaDebit(t *testing.T) {
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
