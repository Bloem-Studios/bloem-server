//go:build integration

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// A focused producer-boundary receipt; HTTP authentication, current host
// authorization, normalization, classification and SQL are all real. This does
// not claim a provider algorithm test or asynchronous all-or-nothing success.
type nativeFiniteOriginReceipt struct{ reached chan context.Context }

func (f *nativeFiniteOriginReceipt) RefreshItem(ctx context.Context, _ string) error {
	f.reached <- ctx
	return nil
}

type nativeFiniteClassQuery struct {
	catalog.NativePhaseQuery
	classes int
}

func (q *nativeFiniteClassQuery) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "bloem_native_item_class(") || strings.Contains(sql, "bloem_native_folder_class(") {
		q.classes++
	}
	return q.NativePhaseQuery.QueryRow(ctx, sql, args...)
}

func TestNativeFiniteActualHTTPAndSelectedAuthDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success", func(t *testing.T, f nativeOnboardingLifecycleFixture) {
		ctx := t.Context()
		var login struct {
			AccessToken string `json:"access_token"`
		}
		f.command("POST", "/api/v1/auth/login", "", "application/json", strings.NewReader(`{"username":"http-admin","password":"acceptance-password"}`), 200, &login)
		if login.AccessToken == "" {
			t.Fatal("actual login did not issue credential")
		}
		var admin int
		var profile string
		if err := f.pool.QueryRow(ctx, "SELECT id FROM users WHERE username='http-admin'").Scan(&admin); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(ctx, "SELECT id FROM user_profiles WHERE user_id=$1 AND is_primary LIMIT 1", admin).Scan(&profile); errors.Is(err, pgx.ErrNoRows) {
			profile = "finite-admin-" + uuid.NewString()
			acceptanceSQL(t, f.pool, "INSERT INTO user_profiles(id,user_id,name,is_primary,organization_id,access_group_id) SELECT $1,$2,'Finite admin',true,o.id,g.id FROM organizations o JOIN access_groups g ON g.organization_id=o.id AND g.is_default WHERE o.is_default", profile, admin)
		} else if err != nil {
			t.Fatal(err)
		}
		var localFolder, hiddenFolder int
		if err := f.pool.QueryRow(ctx, "INSERT INTO media_folders(type,name,owner_id) SELECT 'ebook','finite local',owner_id FROM media_folders WHERE id=$1 RETURNING id", f.created.LibraryID).Scan(&localFolder); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(ctx, "INSERT INTO media_folders(type,name,owner_id) SELECT 'ebook','finite hidden',id FROM resource_owners WHERE organization_id=(SELECT id FROM organizations WHERE slug='http-foreign') LIMIT 1 RETURNING id").Scan(&hiddenFolder); err != nil {
			t.Fatal(err)
		}
		local, hidden := "finite-local-"+uuid.NewString(), "finite-hidden-"+uuid.NewString()
		for _, row := range []struct {
			id     string
			folder int
		}{{local, localFolder}, {hidden, hiddenFolder}} {
			acceptanceSQL(t, f.pool, "INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','finite synthetic')", row.id)
			acceptanceSQL(t, f.pool, "INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", row.id, row.folder)
		}
		var native string
		if err := f.pool.QueryRow(ctx, "SELECT content_id FROM media_files WHERE media_folder_id=$1 ORDER BY id LIMIT 1", f.created.LibraryID).Scan(&native); err != nil {
			t.Fatal(err)
		}
		receipt := &nativeFiniteOriginReceipt{reached: make(chan context.Context, 1)}
		next := f.support.deps
		next.Refresher = receipt
		f.support.rebuild(next, nil)
		do := func(method, path, token, actingProfile, body string) (int, []byte) {
			t.Helper()
			request, err := http.NewRequestWithContext(ctx, method, f.server.URL+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			request.Header.Set("X-Profile-Id", actingProfile)
			request.Header.Set("Content-Type", "application/json")
			response, err := f.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return response.StatusCode, data
		}
		refusal := func(origin context.Context, q catalog.NativePhaseQuery, targets catalog.NativePhaseTargets, want string) {
			t.Helper()
			counted := &nativeFiniteClassQuery{NativePhaseQuery: q}
			err := catalog.RequireNativePhase(origin, counted, targets)
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var denial *catalog.NativePhaseRefusal
			if !errors.As(err, &denial) || denial.Code != want {
				t.Fatalf("selected phase error=%v want=%s", err, want)
			}
			if want == "not_found" || want == "forbidden" || want == "unauthenticated" {
				if counted.classes != 0 {
					t.Fatalf("denied set classified %d targets", counted.classes)
				}
			}
		}
		for _, prefix := range []string{"/api/v1", "/api/v2"} {
			t.Run(prefix, func(t *testing.T) {
				f.command("POST", "/api/v1/auth/login", "", "application/json", strings.NewReader(`{"username":"http-admin","password":"acceptance-password"}`), 200, &login)
				path := prefix + "/libraries/stale-ids/" + local + "/rematch"
				status, data := do("POST", path, login.AccessToken, profile, "")
				if status != 204 {
					t.Fatalf("local rematch %d %s", status, data)
				}
				var origin context.Context
				select {
				case origin = <-receipt.reached:
				case <-time.After(5 * time.Second):
					t.Fatal("existing rematch continuation did not reach refresher")
				}
				if !catalog.NativePhaseRequest(origin) || origin.Err() != nil {
					t.Fatal("actual detached continuation lost origin/lifetime")
				}
				refusal(origin, f.pool, catalog.NativePhaseTargets{ContentIDs: []string{local}}, "")
				for _, ids := range [][]string{{native, hidden}, {hidden, native}} {
					refusal(origin, f.pool, catalog.NativePhaseTargets{ContentIDs: ids}, "not_found")
				}
				for _, ids := range [][]string{{local, native}, {native, local}} {
					refusal(origin, f.pool, catalog.NativePhaseTargets{ContentIDs: ids}, "native_local_operation_unsupported")
				}
				refusal(origin, f.pool, catalog.NativePhaseTargets{Prospective: []catalog.NativePhaseProspective{{ContentID: "finite-absent-" + uuid.NewString(), SourceIDs: []string{local}, LibraryIDs: []int{localFolder}}}}, "")
				t.Run("same-transaction-new-skeleton-file", func(t *testing.T) {
					tx, err := f.pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
					skeleton := "finite-skeleton-" + uuid.NewString()
					var file int
					for _, stmt := range []string{"INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','finite skeleton')", "INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)"} {
						var args []any
						if strings.Contains(stmt, "media_item_libraries") {
							args = []any{skeleton, localFolder}
						} else {
							args = []any{skeleton}
						}
						if _, err = tx.Exec(ctx, stmt, args...); err != nil {
							t.Fatal(err)
						}
					}
					if err = tx.QueryRow(ctx, "INSERT INTO media_files(content_id,media_folder_id,file_path,file_size,container) VALUES($1,$2,$3,1,'epub') RETURNING id", skeleton, localFolder, "/synthetic/"+skeleton+".epub").Scan(&file); err != nil {
						t.Fatal(err)
					}
					var exists bool
					if err = f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1)", skeleton).Scan(&exists); err != nil || exists {
						t.Fatal("skeleton unexpectedly visible outside transaction")
					}
					refusal(origin, tx, catalog.NativePhaseTargets{ContentIDs: []string{skeleton}, FileIDs: []int{file}, LibraryIDs: []int{localFolder}}, "")
				})
				hash, err := bcrypt.GenerateFromPassword([]byte("2468"), bcrypt.MinCost)
				if err != nil {
					t.Fatal(err)
				}
				acceptanceSQL(t, f.pool, "UPDATE user_profiles SET pin_hash=$1 WHERE id=$2", string(hash), profile)
				refusal(origin, f.pool, catalog.NativePhaseTargets{ContentIDs: []string{local, native}}, "forbidden")
				acceptanceSQL(t, f.pool, "UPDATE user_profiles SET pin_hash='' WHERE id=$1", profile)
				status, data = do("POST", prefix+"/libraries/stale-ids/"+native+"/rematch", login.AccessToken, profile, "")
				if status != 409 || !strings.Contains(string(data), "native_repair_unsupported") {
					t.Fatalf("native rematch %d %s", status, data)
				}
				status, data = do("POST", prefix+"/libraries/stale-ids/"+native+"/rematch", "", profile, "")
				if status != 401 || strings.Contains(string(data), "native_") {
					t.Fatalf("unauthenticated disclosure %d %s", status, data)
				}
				status, data = do("PATCH", prefix+"/admin/items/"+native+"/metadata", f.readerToken, "http-reader", `{"title":"denied"}`)
				if status != 403 || strings.Contains(string(data), "native_") {
					t.Fatalf("curation disclosure %d %s", status, data)
				}
				acceptanceSQL(t, f.pool, "UPDATE auth_sessions SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL", admin)
				refusal(origin, f.pool, catalog.NativePhaseTargets{ContentIDs: []string{local, native}}, "unauthenticated")
			})
		}
		// Trusted non-request native enrichment retains the original lower behavior.
		if err := catalog.RequireNativePhase(ctx, f.pool, catalog.NativePhaseTargets{ContentIDs: []string{native}}); err != nil {
			t.Fatal(err)
		}
		var title string
		if err := f.pool.QueryRow(ctx, "SELECT title FROM media_items WHERE content_id=$1", native).Scan(&title); err != nil || title == "denied" {
			t.Fatal(fmt.Sprintf("denied metadata changed: %v", err))
		}
	})
}
