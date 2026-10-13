//go:build integration

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestListFollowOrdinaryMutationGuard(t *testing.T) {
	pool, ctx := coverRecipeDatabase(t), t.Context()
	var user, folder int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role,enabled) VALUES($1,'synthetic','user',true) RETURNING id`, uuid.NewString()).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name,enabled) VALUES('movies','Synthetic guard library',true) RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.NewPostgresProvider(pool).ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	sourceConfig, _ := json.Marshal(map[string]string{"bloem_list_binding": uuid.NewString()})
	personal, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Linked", CollectionType: "synced", SourceConfig: string(sourceConfig)})
	if err != nil {
		t.Fatal(err)
	}
	server, err := catalog.NewLibraryCollectionRepository(pool).Create(ctx, catalog.CreateLibraryCollectionInput{LibraryID: folder, Title: "Linked server", Slug: uuid.NewString(), SourceConfig: sourceConfig})
	if err != nil {
		t.Fatal(err)
	}
	normal, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	guard := listCollectionMutationGuard{pool: pool}
	for _, scope := range []string{"personal", "server"} {
		id := personal.ID
		if scope == "server" {
			id = server.ID
		}
		for _, operation := range []string{"members", "sync"} {
			err = guard.check(ctx, scope, user, id, operation, nil)
			var api *handlers.APIError
			if !errors.As(err, &api) || api.Status != 409 {
				t.Fatalf("linked %s %s accepted: %v", scope, operation, err)
			}
		}
		for _, field := range []string{"source_url", "source_config", "collection_type", "library_ids", "management_mode", "query_definition", "sort_config", "sync_schedule"} {
			if err = guard.check(ctx, scope, user, id, "update", map[string]json.RawMessage{field: json.RawMessage(`null`)}); err == nil {
				t.Fatalf("linked %s retarget field %s accepted", scope, field)
			}
		}
		if err = guard.check(ctx, scope, user, id, "update", map[string]json.RawMessage{"name": json.RawMessage(`"Custom"`), "is_shared": json.RawMessage(`true`), "poster_source_url": json.RawMessage(`"https://example.invalid/art"`)}); err != nil {
			t.Fatal("metadata customization denied", err)
		}
	}
	if err = guard.check(ctx, "personal", user, normal.ID, "members", nil); err != nil {
		t.Fatal("ordinary membership changed", err)
	}
	if err = guard.check(ctx, "personal", user, normal.ID, "update", map[string]json.RawMessage{"sort_config": json.RawMessage(`{}`)}); err != nil {
		t.Fatal("ordinary source settings changed", err)
	}
	// V2 uses service adapters; v1 keeps the original concrete handlers.
	deps := apiv2.Dependencies{PersonalCollections: handlers.NewCollectionHandler(pgstore.NewPostgresProvider(pool)), AdminCollections: handlers.NewLibraryCollectionHandler(catalog.NewLibraryCollectionRepository(pool), nil, nil, nil)}
	wireBloemListMutationGuards(&deps, pool)
	personalService := deps.PersonalCollections.(*listGuardPersonalService)
	url := "https://pastime.example/changed"
	if _, err = personalService.UpdatePersonalCollection(ctx, handlers.PersonalCollectionUpdateCommand{UserID: user, ProfileID: "owner", CollectionID: personal.ID, Request: handlers.PersonalCollectionUpdateRequest{SourceURL: &url}}); err == nil {
		t.Fatal("v2 retarget reached native handler")
	}
	name := "Custom linked name"
	if _, err = personalService.UpdatePersonalCollection(ctx, handlers.PersonalCollectionUpdateCommand{UserID: user, ProfileID: "owner", CollectionID: personal.ID, Request: handlers.PersonalCollectionUpdateRequest{Name: &name}}); err != nil {
		t.Fatal("native linked rename denied", err)
	}
	adminService := deps.AdminCollections.(*listGuardAdminService)
	if err = adminService.AddAdminCollectionItem(ctx, server.ID, "synthetic-item", 0); err == nil {
		t.Fatal("v2 linked server membership accepted")
	}
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var sealed *apiv2.Dependencies
	if newChiRouter(Dependencies{DB: pool, Config: cfg, AppContext: ctx, UserStoreProvider: pgstore.NewPostgresProvider(pool), ClientIPResolver: clientip.NewResolver(nil), NodeID: "list-guard-fixture", v2Wiring: func(deps apiv2.Dependencies) { sealed = &deps }}) == nil || sealed == nil {
		t.Fatal("native router did not seal dependencies")
	}
	if _, ok := sealed.PersonalCollections.(*listGuardPersonalService); !ok {
		t.Fatal("personal guard was overwritten during startup")
	}
	if _, ok := sealed.AdminCollections.(*listGuardAdminService); !ok {
		t.Fatal("server guard was overwritten during startup")
	}
	for _, scope := range []string{"personal", "server"} {
		id := personal.ID
		if scope == "server" {
			id = server.ID
		}
		router := chi.NewRouter()
		router.Use(bloemListMutationMiddleware(pool, scope))
		router.Put("/{id}", func(w http.ResponseWriter, r *http.Request) {
			body, e := io.ReadAll(r.Body)
			if e != nil || string(body) != `{"name":"Custom"}` {
				t.Error("native body changed", e)
			}
			w.WriteHeader(202)
		})
		for _, body := range []string{`{"source_url":null}`, `{"name":"Custom"}`, `{"source_url":null} trailing`} {
			req := httptest.NewRequest(http.MethodPut, "/"+id, strings.NewReader(body))
			req = req.WithContext(apimw.SetClaims(ctx, &auth.Claims{UserID: user}))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := 409
			if body == `{"name":"Custom"}` {
				want = 202
			}
			if strings.HasSuffix(body, " trailing") {
				want = 400
			}
			if response.Code != want {
				t.Fatalf("v1 %s status=%d want=%d body=%s", scope, response.Code, want, response.Body.String())
			}
		}
	}
}
