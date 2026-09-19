package executor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type bloemSectionFixtureKind uint8

const (
	bloemSectionNone bloemSectionFixtureKind = iota
	bloemSectionRead
	bloemSectionReplace
	bloemSectionReset
)

func bloemSectionKind(row scenariocatalog.Row) bloemSectionFixtureKind {
	switch row.Method + " " + strings.TrimSuffix(row.Path, "/") {
	case http.MethodGet + " /api/v1/profile/sections", http.MethodGet + " /api/v1/profile/sections/settings":
		return bloemSectionRead
	case http.MethodPut + " /api/v1/profile/sections":
		return bloemSectionReplace
	case http.MethodDelete + " /api/v1/profile/sections/reset":
		return bloemSectionReset
	default:
		return bloemSectionNone
	}
}

// withBloemSectionRowFixture supplies the library and prior-state overlays
// used by the frozen section packets. Dedicated acceptance tests may supply
// their own afterReseed overlay; their exact setup takes precedence.
func (e *Env) withBloemSectionRowFixture(row scenariocatalog.Row) func() {
	kind := bloemSectionKind(row)
	if !e.HasDatabase() || kind == bloemSectionNone {
		return func() {}
	}
	e.guardScratchDatabase()
	previousBefore, previousAfter := e.beforeReseed, e.afterReseed
	library := newBloemSectionLibrary()
	cleanup := func() {
		if err := library.cleanup(e); err != nil {
			e.t.Fatalf("scenario executor: section library cleanup: %v", err)
		}
	}
	e.beforeReseed = func() {
		cleanup()
		if previousBefore != nil {
			previousBefore()
		}
	}
	e.afterReseed = func() {
		if kind != bloemSectionReplace {
			if err := library.seed(e); err != nil {
				e.t.Fatalf("scenario executor: section library fixture: %v", err)
			}
		}
		if previousAfter != nil {
			previousAfter()
		} else {
			e.seedBloemSectionOverrides(kind)
		}
	}
	closed := false
	restore := func() {
		if closed {
			return
		}
		closed = true
		// Restore hooks even if a failed safety check terminates cleanup.
		defer func() { e.beforeReseed, e.afterReseed = previousBefore, previousAfter }()
		cleanup()
		e.beforeReseed, e.afterReseed = previousBefore, previousAfter
		e.Reseed()
	}
	// A Fatal/Goexit in fixture setup must still tear down our owned library.
	// The row's deferred restore normally runs first; this fallback is idempotent.
	e.t.Cleanup(restore)
	return restore
}

func (e *Env) seedBloemSectionOverrides(kind bloemSectionFixtureKind) {
	if kind != bloemSectionReplace && kind != bloemSectionReset {
		return
	}
	for _, target := range []struct{ user, profile, label string }{
		{fixtureMember, profileSecondary, "secondary"},
		{fixtureMember, profilePrimary, "primary"},
		{fixtureAdmin, profileAdminPrimary, "admin"},
	} {
		if kind == bloemSectionReset && target.user == fixtureAdmin {
			continue
		}
		store, err := e.stores.ForUser(e.ctx, e.users[target.user].ID)
		if err != nil {
			e.t.Fatalf("scenario executor: section fixture user store: %v", err)
		}
		scopes := []string{"home"}
		if kind == bloemSectionReset {
			scopes = append(scopes, "library")
		}
		for _, scope := range scopes {
			library, id, section := "", target.label+"-replacement-original", "fixture-original"
			if kind == bloemSectionReset {
				id, section = target.label+"-"+scope, "fixture-section"
				if scope == "library" {
					library = "7"
				}
			}
			if err := store.SaveSectionOverrides(e.ctx, target.profile, scope, library,
				[]userstore.SectionOverride{{ID: id, SectionID: section, Hidden: true}}); err != nil {
				e.t.Fatalf("scenario executor: section fixture overrides: %v", err)
			}
		}
	}
}

type bloemSectionLibrary struct {
	name, owner string
}

func newBloemSectionLibrary() *bloemSectionLibrary {
	return &bloemSectionLibrary{name: "fixture-profile-sections-" + rand.Text()}
}

func (f *bloemSectionLibrary) seed(e *Env) error {
	if f.owner != "" {
		return errors.New("previous section library has not been removed")
	}
	// Organization ownership supplies normal tenant visibility without a
	// platform entitlement. No filesystem path or media is created.
	return e.pool.QueryRow(e.ctx, `INSERT INTO media_folders (id, type, name, enabled, owner_id)
		SELECT 7, 'movies', $1, true, o.id FROM resource_owners o
		JOIN user_profiles p ON p.organization_id = o.organization_id
		WHERE o.kind = 'organization' AND p.id = $2 AND p.user_id = $3
		RETURNING owner_id::text`, f.name, profilePrimary, e.users[fixtureMember].ID).Scan(&f.owner)
}

func (f *bloemSectionLibrary) cleanup(e *Env) error {
	if f.owner == "" {
		return nil
	}
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(e.ctx) }()
	// Do not let a concurrent insert turn the subsequent folder delete into
	// a cascading deletion of media. The ordinary guard still runs unchanged
	// immediately after this owned-fixture cleanup, before any reseed.
	if _, err := tx.Exec(e.ctx, `LOCK TABLE media_items, media_files, media_folders IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var items, files, foreignFolders int
	if err := tx.QueryRow(e.ctx, `SELECT
		(SELECT count(*) FROM media_items),
		(SELECT count(*) FROM media_files),
		(SELECT count(*) FROM media_folders WHERE
			NOT (id = 7 AND name = $1 AND owner_id = $2::uuid AND type = 'movies' AND enabled IS TRUE))`,
		f.name, f.owner).Scan(&items, &files, &foreignFolders); err != nil {
		return err
	}
	if items != 0 || files != 0 || foreignFolders != 0 {
		return fmt.Errorf("refusing cleanup with %d media items, %d media files and %d foreign library folders", items, files, foreignFolders)
	}
	deleted, err := tx.Exec(e.ctx, `DELETE FROM media_folders
		WHERE id = 7 AND name = $1 AND owner_id = $2::uuid AND type = 'movies' AND enabled IS TRUE`, f.name, f.owner)
	if err != nil {
		return err
	}
	if deleted.RowsAffected() != 1 {
		return errors.New("owned section library is missing; refusing to assume cleanup succeeded")
	}
	if err := tx.Commit(e.ctx); err != nil {
		return err
	}
	f.owner = ""
	return nil
}
