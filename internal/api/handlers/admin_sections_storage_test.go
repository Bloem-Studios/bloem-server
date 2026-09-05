package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// A mixed provider must not inherit the PostgreSQL-only reset capability even
// when one of its account stores happens to use PostgreSQL.
type mixedSectionProvider struct{ userstore.UserStoreProvider }

func (mixedSectionProvider) ForUser(context.Context, int) (userstore.UserStore, error) {
	panic("unsupported reset must reject before consulting account stores")
}

func TestRestoreSectionDefaultsRejectsUnsupportedProfileResetBeforeWrites(t *testing.T) {
	for name, provider := range map[string]userstore.UserStoreProvider{
		"unavailable":  nil,
		"sqlite":       userdb.NewSQLiteProvider(nil),
		"mixed":        mixedSectionProvider{pgstore.NewPostgresProvider(nil)},
		"nil postgres": (*pgstore.PostgresProvider)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			// No repository is wired: an attempted definition read or write would panic.
			h := &SectionHandler{StoreProvider: provider}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sections/restore-defaults", strings.NewReader(`{"scope":"home","reset_profiles":true}`))
			rec := httptest.NewRecorder()
			h.HandleRestoreDefaults(rec, req)
			if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "capability_unsupported") {
				t.Fatalf("unsupported reset response: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLegacyCollectionDeleteChecksSectionReferencesWithoutHandlerPrecheckDB(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	repo := catalog.NewLibraryCollectionRepository(f.pool)
	h := NewLibraryCollectionHandler(repo, nil, catalog.NewItemRepository(f.pool), 0, nil, nil)
	collection, err := h.CreateAdminCollection(t.Context(), AdminCollectionCreate{LibraryID: f.library, Title: "Referenced collection", CollectionType: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	sectionRepo := sections.NewRepository(f.pool)
	cfg, err := json.Marshal(sections.SectionCollectionConfig{LibraryCollectionID: collection.ID})
	if err != nil {
		t.Fatal(err)
	}
	section, err := sectionRepo.Create(t.Context(), &sections.PageSection{Scope: "library", LibraryID: &f.library, SectionType: sections.SectionCollection, Title: "Reference", Config: cfg, Enabled: true, ItemLimit: 20})
	if err != nil {
		t.Fatal(err)
	}
	// SectionRepo is deliberately absent from h. The invariant belongs to the
	// storage transaction, including callers such as template replacement.
	if err := h.deleteServerCollection(t.Context(), collection.ID); !errors.Is(err, catalog.ErrLibraryCollectionInUse) {
		t.Fatalf("delete referenced collection: %v", err)
	}
	if _, err := repo.GetByID(t.Context(), collection.ID); err != nil {
		t.Fatal(err)
	}
	if err := sectionRepo.Delete(t.Context(), section.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.deleteServerCollection(t.Context(), collection.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSectionDefaultsPostgresResetAndSQLiteDefinitionsDB(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	repo := sections.NewRepository(f.pool)
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%v", reset), func(t *testing.T) {
			section, err := repo.Create(t.Context(), &sections.PageSection{Scope: "library", LibraryID: &f.library, SectionType: sections.SectionRecentlyAdded, Title: "Before restore", Enabled: true, ItemLimit: 20})
			if err != nil {
				t.Fatal(err)
			}
			key := fmt.Sprintf("section_overrides:library:%d", f.library)
			f.exec(t, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,'[]') ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value`, f.account, key)
			var provider userstore.UserStoreProvider = userdb.NewSQLiteProvider(nil)
			if reset {
				provider = pgstore.NewPostgresProvider(f.pool)
			}
			h := &SectionHandler{repo: repo, StoreProvider: provider}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sections/restore-defaults", strings.NewReader(fmt.Sprintf(`{"scope":"library","library_id":%d,"reset_profiles":%v}`, f.library, reset)))
			rec := httptest.NewRecorder()
			h.HandleRestoreDefaults(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
			}
			if _, err := repo.GetByID(t.Context(), section.ID); !errors.Is(err, sections.ErrSectionNotFound) {
				t.Fatalf("old definition remains: %v", err)
			}
			var overrides int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM user_settings WHERE user_id=$1 AND key=$2`, f.account, key).Scan(&overrides); err != nil {
				t.Fatal(err)
			}
			if (overrides == 0) != reset {
				t.Fatalf("reset=%v remaining overrides=%d", reset, overrides)
			}
		})
	}
}
