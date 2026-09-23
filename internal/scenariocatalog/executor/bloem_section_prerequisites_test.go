package executor

import (
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestBloemSectionFixtureRows(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bloemSectionFixtureKind
	}{
		{http.MethodGet, "/api/v1/profile/sections/", bloemSectionRead},
		{http.MethodGet, "/api/v1/profile/sections/settings", bloemSectionRead},
		{http.MethodPut, "/api/v1/profile/sections/", bloemSectionReplace},
		{http.MethodDelete, "/api/v1/profile/sections/reset", bloemSectionReset},
		{http.MethodGet, "/api/v1/profile/sections/flags", bloemSectionNone},
		{http.MethodPut, "/api/v1/profile/sections/settings", bloemSectionNone},
		{http.MethodGet, "/api/v1/profiles/", bloemSectionNone},
	} {
		if got := bloemSectionKind(scenariocatalog.Row{Method: tc.method, Path: tc.path}); got != tc.want {
			t.Errorf("fixture kind for %s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestBloemSectionPrerequisites(t *testing.T) {
	e := New(t)
	if !e.HasDatabase() {
		t.Fatal(DatabaseEnv + " is required for section fixture regression coverage")
	}
	for _, kind := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/profile/sections/"},
		{http.MethodPut, "/api/v1/profile/sections/"},
		{http.MethodDelete, "/api/v1/profile/sections/reset"},
	} {
		t.Run(kind.method, func(t *testing.T) {
			func() {
				restore := e.withBloemSectionRowFixture(scenariocatalog.Row{Method: kind.method, Path: kind.path})
				defer restore()
				// The library must be re-created for each fixture state, owned by
				// the reseeded profile's organization. Reseed may restore the same
				// organization identity from its snapshot, so the row's inserting
				// transaction (xmin) is what shows it was not retained.
				var previousGeneration string
				for range 2 {
					e.Reseed()
					if kind.method != http.MethodPut {
						var generation string
						if err := e.pool.QueryRow(e.ctx, `SELECT f.xmin::text FROM media_folders f
							JOIN resource_owners o ON o.id = f.owner_id
							JOIN user_profiles p ON p.organization_id = o.organization_id
							WHERE f.id = 7 AND o.kind = 'organization' AND p.id = $1`, profileSecondary).Scan(&generation); err != nil {
							t.Fatal(err)
						}
						if generation == previousGeneration {
							t.Fatal("section library was retained across reseed instead of re-created")
						}
						previousGeneration = generation
					}
					store, err := e.stores.ForUser(e.ctx, e.users[fixtureMember].ID)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := store.ListSectionOverrides(e.ctx, profileSecondary, "home", "")
					if err != nil {
						t.Fatal(err)
					}
					if kind.method == http.MethodGet {
						if len(rows) != 0 {
							t.Fatal("read fixture unexpectedly populated section overrides")
						}
					} else {
						want := "secondary-replacement-original"
						if kind.method == http.MethodDelete {
							want = "secondary-home"
							library, err := store.ListSectionOverrides(e.ctx, profileSecondary, "library", "7")
							if err != nil || len(library) != 1 || library[0].ID != "secondary-library" {
								t.Fatalf("reset fixture did not supply independent library overlays: %v", err)
							}
						}
						if len(rows) != 1 || rows[0].ID != want || !rows[0].Hidden {
							t.Fatal("section fixture did not supply the frozen prior state")
						}
					}
				}
			}()
			if e.afterReseed != nil || e.beforeReseed != nil {
				t.Fatal("section fixture survived row teardown")
			}
			e.guardScratchDatabase()
		})
	}

	// An acceptance test's explicit overlay remains authoritative.
	e.afterReseed = func() {
		store, err := e.stores.ForUser(e.ctx, e.users[fixtureMember].ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveSectionOverrides(e.ctx, profileSecondary, "home", "", []userstore.SectionOverride{{ID: "dedicated-overlay"}}); err != nil {
			t.Fatal(err)
		}
	}
	func() {
		restore := e.withBloemSectionRowFixture(scenariocatalog.Row{Method: http.MethodDelete, Path: "/api/v1/profile/sections/reset"})
		defer restore()
		e.Reseed()
		store, err := e.stores.ForUser(e.ctx, e.users[fixtureMember].ID)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListSectionOverrides(e.ctx, profileSecondary, "home", "")
		if err != nil || len(rows) != 1 || rows[0].ID != "dedicated-overlay" {
			t.Fatalf("generic fixture replaced the dedicated overlay: %v", err)
		}
	}()
	e.afterReseed = nil
	e.Reseed()
	checkBloemSectionCleanupRefusals(t, e)
}

func checkBloemSectionCleanupRefusals(t *testing.T, e *Env) {
	t.Helper()
	library := newBloemSectionLibrary()
	if err := library.seed(e); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := library.cleanup(e); err != nil {
			t.Error(err)
		}
	}()
	// Every foreign media signal must stop cleanup. A file under our own
	// folder must survive too: deleting the folder would cascade that file.
	for _, tc := range []struct{ create, remove string }{
		{`INSERT INTO media_folders (id, type, name, owner_id)
			SELECT 8, 'movies', 'fixture-foreign-section-library', owner_id FROM media_folders WHERE id = 7`,
			`DELETE FROM media_folders WHERE id = 8 AND name = 'fixture-foreign-section-library'`},
		{`INSERT INTO media_items (content_id, type, title, status)
			VALUES ('fixture-section-cleanup-refusal', 'movie', 'Fixture cleanup refusal', 'matched')`,
			`DELETE FROM media_items WHERE content_id = 'fixture-section-cleanup-refusal'`},
		{`INSERT INTO media_files (media_folder_id, file_path)
			VALUES (7, 'fixture-section-cleanup-refusal.mkv')`,
			`DELETE FROM media_files WHERE media_folder_id = 7 AND file_path = 'fixture-section-cleanup-refusal.mkv'`},
	} {
		func() {
			e.mustExec(tc.create)
			defer e.mustExec(tc.remove)
			if err := library.cleanup(e); err == nil {
				t.Fatal("section cleanup accepted foreign media")
			}
			var count int
			if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM media_folders WHERE id = 7`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("refused cleanup deleted the owned library: %v", err)
			}
		}()
	}
	func() {
		e.mustExec(`UPDATE media_folders SET name = 'fixture-replaced-section-library' WHERE id = 7`)
		defer e.mustExec(`UPDATE media_folders SET name = $1 WHERE id = 7`, library.name)
		if err := library.cleanup(e); err == nil {
			t.Fatal("section cleanup accepted a changed library identity")
		}
	}()
}
