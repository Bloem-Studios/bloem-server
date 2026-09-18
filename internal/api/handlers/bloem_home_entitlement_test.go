package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

// An empty platform library and withdrawal of its last organization grant must
// neither break home reads nor restore library access through the owner's role.
func TestBloemHomeSectionsWithWithdrawnLibraryGrant(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool := newCompatAdapterDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	var accountID int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,email,password_hash,role,enabled)
		VALUES ('home-owner','home-owner@example.test','x','admin',true) RETURNING id`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `UPDATE organizations SET status='active',owner_account_id=$1
		WHERE is_default RETURNING id`, accountID).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
		VALUES ($1,$2,'active','admin') ON CONFLICT (organization_id,account_id)
		DO UPDATE SET status='active',legacy_role='admin'`, organizationID, accountID); err != nil {
		t.Fatal(err)
	}
	groups := access.NewGroupStore(pool)
	group, err := groups.Create(ctx, organizationID, access.CreateGroupInput{Name: "Home viewers", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	stores := pgstore.NewPostgresProvider(pool)
	store, err := stores.ForUser(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	profileID := uuid.NewString()
	if err := store.CreateProfile(ctx, userstore.Profile{ID: profileID, Name: "Parent", IsPrimary: true,
		OrganizationID: organizationID.String(), AccessGroupID: &group.ID}); err != nil {
		t.Fatal(err)
	}
	sectionRepo := sections.NewRepository(pool)
	if _, err := sectionRepo.RestoreDefaults(ctx, "home", nil, sections.DefaultHomeSections(nil)); err != nil {
		t.Fatal(err)
	}
	// Match the acceptance fixture: column defaults, no paths, no media. The
	// compatibility trigger creates the default organization's initial grant.
	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type,name)
		VALUES ('movies','Granted empty library') RETURNING id`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	clock := recipes.FixedClock(time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	promos := promotions.NewService(pool, clock, stores)
	campaign, err := promos.Create(ctx, accountID, promotions.Input{
		Surfaces: []string{"home"}, Headline: "Server message", ImageURL: "https://example.test/message.jpg",
		Deeplink: "bloem://home", StartsAt: clock.Now().Add(-time.Hour), EndsAt: clock.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := tenancy.NewResolver(tenancy.NewStore(pool)).Resolve(ctx, accountID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx = tenancy.WithContext(ctx, tenant)
	engine, err := policy.NewEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resources := resourcetenancy.NewStore(pool)
	users := auth.NewUserRepository(pool)
	resolver := policy.NewViewerResolver(users, stores, nil, policy.NewPDP(engine), resources, groups)
	fetcher := sections.NewFetcher(pool)
	fetcher.StoreProvider = stores
	fetcher.Promotions = promos
	h := NewSectionHandler(sectionRepo, fetcher)
	h.StoreProvider, h.UserRepo, h.AccessGroups = stores, users, groups
	h.FolderRepo, h.Promotions = catalog.NewFolderRepository(pool), promos

	for _, stage := range []string{"active", "suspended", "restored", "withdrawn"} {
		t.Run(stage, func(t *testing.T) {
			switch stage {
			case "suspended":
				_, err = resources.SetLibraryEntitlementStatus(ctx, organizationID, int64(folderID), 1, resourcetenancy.EntitlementSuspended)
			case "restored":
				_, err = resources.SetLibraryEntitlementStatus(ctx, organizationID, int64(folderID), 2, resourcetenancy.EntitlementActive)
			case "withdrawn":
				err = resources.DeleteLibraryEntitlement(ctx, organizationID, int64(folderID), 3)
			}
			if err != nil {
				t.Fatal(err)
			}
			scope, err := resolver.Resolve(ctx, access.ResolveInput{UserID: accountID, ProfileID: profileID})
			if err != nil {
				t.Fatalf("resolve viewer: %v", err)
			}
			wantIDs := []int{}
			if stage == "active" || stage == "restored" {
				wantIDs = []int{folderID}
			}
			if !scope.LibrariesRestricted || scope.AllowedLibraryIDs == nil || !slices.Equal(scope.AllowedLibraryIDs, wantIDs) {
				t.Fatalf("library scope: restricted=%t ids=%#v, want %v", scope.LibrariesRestricted, scope.AllowedLibraryIDs, wantIDs)
			}
			viewerCtx := access.SetScope(ctx, scope)
			viewerCtx = apimw.SetClaims(viewerCtx, &auth.Claims{UserID: accountID, Role: "admin"})
			viewerCtx = apimw.SetProfileID(viewerCtx, profileID)
			resolved, ids, filter, _, err := h.loadResolvedHomeSections(viewerCtx)
			if err != nil {
				t.Fatalf("load home sections: %v", err)
			}
			for _, section := range resolved {
				// Check the fetcher too: the service's existing fetch-error
				// fallback must not disguise a failed query as an empty row.
				items, err := fetcher.FetchOne(viewerCtx, section, nil, ids, accountID, profileID, filter)
				if err != nil || len(items.Items) != 0 {
					t.Fatalf("fetch %s: items=%d error=%v", section.SectionType, len(items.Items), err)
				}
			}
			layout, err := h.HomeLayout(WithHomePromotions(viewerCtx, true))
			if err != nil || len(layout.Sections) != 5 {
				t.Fatalf("layout: sections=%d error=%v", len(layout.Sections), err)
			}
			for _, section := range layout.Sections {
				// V1 uses this handler; V2 invokes HomeSectionItems directly.
				routeCtx := chi.NewRouteContext()
				routeCtx.URLParams.Add("id", section.ID)
				req := httptest.NewRequest(http.MethodGet, "/api/v1/home/sections/"+section.ID+"/items?promoted=1", nil)
				req = req.WithContext(context.WithValue(viewerCtx, chi.RouteCtxKey, routeCtx))
				rec := httptest.NewRecorder()
				h.HandleHomeSectionItems(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("section %s: status=%d body=%s", section.ID, rec.Code, rec.Body.String())
				}
				var wire promotedHomeWire
				if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
					t.Fatal(err)
				}
				if wire.Section == nil {
					t.Fatal("missing section response")
				}
				if section.ID == SystemPromotedSectionID {
					if len(wire.Section.Items) != 1 || wire.Section.Items[0].ContentID != campaign.ID {
						t.Fatalf("promotion missing: %s", rec.Body.String())
					}
				} else if len(wire.Section.Items) != 0 {
					t.Fatalf("empty library returned media: %s", rec.Body.String())
				}
			}
			if len(wantIDs) == 0 {
				_, err := h.LibraryLayout(viewerCtx, folderID)
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
					t.Fatalf("inaccessible library layout: %v, want 404", err)
				}
			}
		})
	}
}
