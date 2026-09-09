package requests

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/metadata/tmdb"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/organizations"
	"github.com/jackc/pgx/v5/pgxpool"
)

type presenceUsers struct{}

func (presenceUsers) GetByID(_ context.Context, id int) (*models.User, error) {
	return &models.User{ID: id, OrganizationID: int64(id)}, nil
}

// Exercise request entry points with real organization and catalog queries,
// alternating viewers against one canonical title and the same service.
func TestRequestPresenceOrganizationLibraries(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), `
 CREATE TEMP TABLE organizations(id bigint PRIMARY KEY,status text,access_revision bigint);
 CREATE TEMP TABLE media_folders(id int PRIMARY KEY,organization_id bigint);
 CREATE TEMP TABLE organization_library_grants(organization_id bigint,media_folder_id int);
 CREATE TEMP TABLE media_items(content_id text PRIMARY KEY,type text);
 CREATE TEMP TABLE media_item_libraries(content_id text,media_folder_id int);
 INSERT INTO organizations VALUES(1,'active',1),(2,'active',1);
 INSERT INTO media_folders VALUES(11,1),(22,2),(33,NULL);
 INSERT INTO organization_library_grants VALUES(1,33),(2,33);
 INSERT INTO media_items VALUES('movie-42','movie');
 INSERT INTO media_item_libraries VALUES('movie-42',22);
 `)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeStore()
	service := NewService(store, &fakeTMDBClient{
		page:   &tmdb.MediaPage{Results: []tmdb.MediaResult{{ID: 42, MediaType: "movie", Title: "Shared title"}}},
		detail: &tmdb.MediaDetail{ID: 42, Title: "Shared title"},
	}, &fakePresence{available: map[MediaType]map[int]bool{MediaTypeMovie: {42: true}}})
	resolver := organizations.NewViewerResolver(presenceUsers{}, organizations.NewRepository(pool), presenceScopeFunc(func(_ context.Context, in access.ResolveInput) (access.Scope, error) {
		return access.Scope{UserID: in.UserID, ProfileID: in.ProfileID}, nil
	}))
	service.SetPresenceAccess(resolver, catalog.NewItemRepository(pool))
	service.SetUserRepository(presenceUsers{})
	check := func(user int, want Availability) {
		t.Helper()
		viewer := Viewer{UserID: user, ProfileID: "profile"}
		page, err := service.Search(t.Context(), viewer, "title", MediaTypeMovie, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Results) != 1 || page.Results[0].Availability != want || (page.Results[0].LibraryContentID != "") != (want == AvailabilityAvailable) {
			t.Fatalf("user %d search: %+v", user, page)
		}
		detail, err := service.GetDetail(t.Context(), viewer, MediaTypeMovie, 42)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Availability != want || (detail.LibraryContentID != "") != (want == AvailabilityAvailable) {
			t.Fatalf("user %d detail: %+v", user, detail)
		}
	}
	check(1, AvailabilityMissing)
	check(2, AvailabilityAvailable)
	check(1, AvailabilityMissing)
	input := CreateRequestInput{MediaType: MediaTypeMovie, TMDBID: 42, Title: "Shared title"}
	if _, err = service.CreateRequest(t.Context(), Viewer{UserID: 2, ProfileID: "profile"}, input); !errors.Is(err, ErrAlreadyAvailable) {
		t.Fatalf("accessible create: %v", err)
	}
	if _, err = service.CreateRequest(t.Context(), Viewer{UserID: 1, ProfileID: "profile"}, input); err != nil {
		t.Fatalf("private copy blocked request: %v", err)
	}
	if _, err = pool.Exec(t.Context(), `INSERT INTO media_item_libraries VALUES('movie-42',33)`); err != nil {
		t.Fatal(err)
	}
	check(1, AvailabilityAvailable)
	if _, err = pool.Exec(t.Context(), `DELETE FROM organization_library_grants WHERE organization_id=1`); err != nil {
		t.Fatal(err)
	}
	check(1, AvailabilityMissing)
	check(2, AvailabilityAvailable)
	if _, err = pool.Exec(t.Context(), `UPDATE organizations SET status='suspended' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Search(t.Context(), Viewer{UserID: 1, ProfileID: "profile"}, "title", MediaTypeMovie, 1); err == nil {
		t.Fatal("suspended search succeeded")
	}
}
