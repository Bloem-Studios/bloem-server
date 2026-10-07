//go:build integration

package sections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These are ordinary/local lower-layer controls, not native actor grants.
func nativeSectionsDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	path := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read private fixture")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(b)), false)
	if err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	var cloneName string
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Logf("ownedUUID cleanup %s verified", cloneName)
		}
	})
	cloneCfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	templateCfg, templateErr := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil || templateErr != nil || cloneCfg.ConnConfig.Database == templateCfg.ConnConfig.Database ||
		!strings.HasPrefix(cloneCfg.ConnConfig.Database, "bloem_storage_test_") {
		t.Fatal("clone DSN must identify a separate UUID-owned database")
	}
	cloneName = cloneCfg.ConnConfig.Database
	suffix := cloneName[strings.LastIndex(cloneName, "_")+1:]
	if len(suffix) != 32 {
		t.Fatal("clone database is not UUID-owned")
	}
	if _, err := uuid.Parse(suffix); err != nil {
		t.Fatal("clone database is not UUID-owned")
	}
	pool, err = pgxpool.NewWithConfig(t.Context(), cloneCfg)
	if err != nil {
		t.Fatal("open private clone")
	}
	var actual string
	if err := pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cloneName || actual == templateCfg.ConnConfig.Database {
		t.Fatal("connected clone identity unverified; refusing fixture writes")
	}
	t.Logf("ownedUUID create %s caller identity verified", cloneName)
	if err := bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			t.Fatalf("verified clone preparation failed (SQLSTATE %s)", pe.Code)
		}
		t.Fatal("verified clone preparation failed; root protected diagnostic required")
	}
	return pool, dsn
}

func sectionsLocalLibrary(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebook", Name: "Local section control"})
	if err != nil {
		t.Fatal(err)
	}
	return folder.ID
}

func localSectionOwner(id int) NativeLibraryAuthorizeTx {
	return func(ctx context.Context, tx pgx.Tx) error {
		var same bool
		// A real retained owner witness on an ordinary folder, never a native grant.
		err := tx.QueryRow(ctx, `SELECT f.enabled AND f.type='ebook' AND o.kind IN ('platform','organization') AND NOT EXISTS(SELECT 1 FROM bloem_native_libraries n WHERE n.folder_id=f.id) FROM media_folders f JOIN resource_owners o ON o.id=f.owner_id WHERE f.id=$1 FOR SHARE OF f,o`, id).Scan(&same)
		if err != nil {
			return err
		}
		if !same {
			return errors.New("local owner unavailable")
		}
		return nil
	}
}

func sectionCount(t *testing.T, p *pgxpool.Pool, id int) int {
	t.Helper()
	var count int
	if err := p.QueryRow(t.Context(), "SELECT count(*) FROM page_sections WHERE library_id=$1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// Snapshots read stored rows, not expected values reconstructed by the seeder.
func nativeSectionRowsSnapshot(t *testing.T, p *pgxpool.Pool, ids []string) string {
	t.Helper()
	var snapshot string
	if err := p.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(
 jsonb_build_object('row',to_jsonb(s),'revision',v.revision) ORDER BY s.id),'[]'::jsonb)::text
 FROM page_sections s LEFT JOIN page_section_revisions v ON v.section_id=s.id
 WHERE s.id=ANY($1)`, ids).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func nativeSectionScopeSnapshot(t *testing.T, p *pgxpool.Pool, scope string, libraryID *int) string {
	t.Helper()
	id := 0
	if libraryID != nil {
		id = *libraryID
	}
	var snapshot string
	if err := p.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'sections',(SELECT COALESCE(jsonb_agg(jsonb_build_object('row',to_jsonb(s),'revision',v.revision)
 ORDER BY s.id),'[]'::jsonb) FROM page_sections s LEFT JOIN page_section_revisions v ON v.section_id=s.id
 WHERE s.scope=$1 AND COALESCE(s.library_id,0)=$2),
 'scope_revision',(SELECT revision FROM page_section_scope_revisions WHERE scope=$1 AND library_id=$2))::text`, scope, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNativeLibrarySectionsLowerLayerDB(t *testing.T) {
	p, _ := nativeSectionsDB(t)
	r := NewRepository(p)
	t.Run("nil callbacks fail before revision writes", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		for _, err := range []error{
			r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, nil),
			r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Books", nil),
		} {
			var e *catalog.NativeOnboardingError
			if !errors.As(err, &e) || e.Code != "native_storage_unavailable" {
				t.Fatalf("nil authorizer admitted: %v", err)
			}
		}
		if sectionCount(t, p, id) != 0 {
			t.Fatal("nil authorizer wrote library rows")
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM page_section_scope_revisions WHERE scope='library' AND library_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("nil authorizer wrote scope revision")
		}
		home, err := r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(home) != 0 {
			t.Fatalf("nil authorizer wrote home rows: %+v %v", home, err)
		}
	})
	t.Run("home serialization retry uses a fresh transaction and avoids duplicates", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		auth := localSectionOwner(id)
		calls := 0
		var first pgx.Tx
		err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Books", func(ctx context.Context, tx pgx.Tx) error {
			calls++
			if err := auth(ctx, tx); err != nil {
				return err
			}
			if calls == 1 {
				first = tx
				_, err := tx.Exec(ctx, "DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='40001', MESSAGE='owned home retry fault'; END $$")
				return err
			}
			if tx == first {
				t.Error("home retry reused transaction")
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("home retry: calls=%d err=%v", calls, err)
		}
		rows, err := r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(rows) != 2 {
			t.Fatalf("retried home rows: %+v %v", rows, err)
		}
	})

	t.Run("denial rolls back scope witnesses and rows", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		deny := errors.New("revoked")
		err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, func(context.Context, pgx.Tx) error { return deny })
		if !errors.Is(err, deny) {
			t.Fatalf("denial lost: %v", err)
		}
		if sectionCount(t, p, id) != 0 {
			t.Fatal("denied seed wrote sections")
		}
		var n int
		if err := p.QueryRow(t.Context(), "SELECT count(*) FROM page_section_scope_revisions WHERE scope='library' AND library_id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatal("denied seed committed scope witness")
		}
	})
	t.Run("ebook defaults preserve custom sections and revisions", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		auth := localSectionOwner(id)
		if err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, auth); err != nil {
			t.Fatal(err)
		}
		rows, err := r.ListByScopeAll(t.Context(), "library", &id)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 5 || rows[0].Title != "Continue Reading" {
			t.Fatalf("ebook defaults: %+v", rows)
		}
		before, err := r.ScopeRevision(t.Context(), "library", &id)
		if err != nil || before <= 1 {
			t.Fatalf("missing scope revision: %d %v", before, err)
		}
		// A repeated seed must not rewrite any existing row or revision witness.
		for i, existing := range rows {
			row := *existing
			row.Title = "Reader's custom row " + strconv.Itoa(i)
			row.Position = 11 * (i + 1)
			row.ItemLimit = 21 + i
			row.Enabled = i%2 == 0
			row.Featured = i == 0
			row.Config = json.RawMessage(`{"continue_type":"ebook","custom":"preserve-library-config"}`)
			if err := r.Update(t.Context(), &row); err != nil {
				t.Fatal(err)
			}
			rev, err := r.SectionRevision(t.Context(), row.ID)
			if err != nil || rev <= 1 {
				t.Fatalf("missing row revision: %d %v", rev, err)
			}
		}
		beforeRepeat := nativeSectionScopeSnapshot(t, p, "library", &id)
		if err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, auth); err != nil {
			t.Fatal(err)
		}
		if after := nativeSectionScopeSnapshot(t, p, "library", &id); after != beforeRepeat {
			t.Fatalf("repeat rewrote existing library rows or revisions\nbefore=%s\nafter=%s", beforeRepeat, after)
		}
		customID := sectionsLocalLibrary(t, p)
		custom, err := r.Create(t.Context(), &PageSection{Scope: "library", LibraryID: &customID, SectionType: SectionRecentlyAdded, Title: "Only my row", Position: 47, Featured: true, ItemLimit: 29, Enabled: false, Config: json.RawMessage(`{"custom":"only-my-row"}`)})
		if err != nil {
			t.Fatal(err)
		}
		beforeCustom := nativeSectionScopeSnapshot(t, p, "library", &customID)
		if err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), customID, localSectionOwner(customID)); err != nil {
			t.Fatal(err)
		}
		got, err := r.ListByScopeAll(t.Context(), "library", &customID)
		if err != nil || len(got) != 1 || got[0].ID != custom.ID {
			t.Fatalf("custom scope replaced: %+v %v", got, err)
		}
		if after := nativeSectionScopeSnapshot(t, p, "library", &customID); after != beforeCustom {
			t.Fatalf("repeat rewrote custom-only scope\nbefore=%s\nafter=%s", beforeCustom, after)
		}
	})
	t.Run("fresh authorization on serialized retry can revoke", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		auth := localSectionOwner(id)
		calls := 0
		var first pgx.Tx
		denied := errors.New("revoked on retry")
		err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
			calls++
			if err := auth(ctx, tx); err != nil {
				return err
			}
			if calls == 1 {
				first = tx
				_, err := tx.Exec(ctx, "DO $$ BEGIN RAISE EXCEPTION USING ERRCODE='40001', MESSAGE='owned serialization fault'; END $$")
				return err
			}
			if tx == first {
				t.Error("retry reused transaction")
			}
			return denied
		})
		if !errors.Is(err, denied) || calls != 2 {
			t.Fatalf("retry authorization: calls=%d err=%v", calls, err)
		}
		if sectionCount(t, p, id) != 0 {
			t.Fatal("revoked retry wrote rows")
		}
	})
	t.Run("home stage rollback preserves committed library and repeat preserves complete custom rows", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		auth := localSectionOwner(id)
		if err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, auth); err != nil {
			t.Fatal(err)
		}
		denied := errors.New("home revoked")
		if err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Books", func(context.Context, pgx.Tx) error { return denied }); !errors.Is(err, denied) {
			t.Fatalf("home denial: %v", err)
		}
		if sectionCount(t, p, id) != 5 {
			t.Fatal("later denial removed committed library")
		}
		home, err := r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(home) != 0 {
			t.Fatalf("denied home wrote rows: %+v %v", home, err)
		}
		if err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Books", auth); err != nil {
			t.Fatal(err)
		}
		home, err = r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(home) != 2 {
			t.Fatalf("home kinds: %+v %v", home, err)
		}
		for i, existing := range home {
			row := *existing
			row.Title = "Custom home title " + strconv.Itoa(i)
			row.Position = 1001 + i*1001
			row.ItemLimit = 31 + i
			row.Enabled = i != 0
			row.Featured = i == 0
			var config map[string]any
			if err := json.Unmarshal(row.Config, &config); err != nil {
				t.Fatal(err)
			}
			config["custom"] = "preserve-home-config"
			row.Config, err = json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Update(t.Context(), &row); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.Create(t.Context(), &PageSection{Scope: "home", Position: 77, SectionType: SectionRandom,
			Title: "Unrelated custom home", ItemLimit: 17, Enabled: false,
			Config: json.RawMessage(`{"custom":"unrelated-home"}`)}); err != nil {
			t.Fatal(err)
		}
		beforeRepeat := nativeSectionScopeSnapshot(t, p, "home", nil)
		if err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "New name", auth); err != nil {
			t.Fatal(err)
		}
		if after := nativeSectionScopeSnapshot(t, p, "home", nil); after != beforeRepeat {
			t.Fatalf("repeat rewrote generated or unrelated custom home rows/revisions\nbefore=%s\nafter=%s", beforeRepeat, after)
		}
		home, err = r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(home) != 2 {
			t.Fatal("repeat duplicated home kinds")
		}
	})
	t.Run("home seed appends only a genuinely missing kind", func(t *testing.T) {
		id := sectionsLocalLibrary(t, p)
		auth := localSectionOwner(id)
		config := json.RawMessage(fmt.Sprintf(`{"filter_library_id":%d,"generated_source":"home_library_recent","custom":"keep-existing-added"}`, id))
		added, err := r.Create(t.Context(), &PageSection{Scope: "home", Position: 3003,
			SectionType: SectionRecentlyAdded, Title: "Existing custom added", ItemLimit: 27,
			Featured: true, Enabled: false, Config: config})
		if err != nil {
			t.Fatal(err)
		}
		existing, err := r.ListByScopeAll(t.Context(), "home", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(existing))
		maxPosition := 0
		for _, row := range existing {
			ids = append(ids, row.ID)
			if row.Position > maxPosition {
				maxPosition = row.Position
			}
		}
		beforeRows := nativeSectionRowsSnapshot(t, p, ids)
		beforeRevision, err := r.ScopeRevision(t.Context(), "home", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Missing kind books", auth); err != nil {
			t.Fatal(err)
		}
		if after := nativeSectionRowsSnapshot(t, p, ids); after != beforeRows {
			t.Fatalf("appending missing kind rewrote pre-existing home rows/revisions\nbefore=%s\nafter=%s", beforeRows, after)
		}
		generated, err := r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
		if err != nil || len(generated) != 2 {
			t.Fatalf("missing kind was not appended exactly once: %+v %v", generated, err)
		}
		for _, row := range generated {
			if row.ID == added.ID {
				continue
			}
			if row.SectionType != SectionRecentlyReleased || row.Title != "Recently Released in Missing kind books" || row.Position != maxPosition+1 {
				t.Fatalf("wrong appended kind/position: %+v", row)
			}
		}
		afterRows, err := r.ListByScopeAll(t.Context(), "home", nil)
		if err != nil || len(afterRows) != len(existing)+1 {
			t.Fatalf("append touched row count: %d %v", len(afterRows), err)
		}
		afterRevision, err := r.ScopeRevision(t.Context(), "home", nil)
		if err != nil || afterRevision <= beforeRevision {
			t.Fatalf("append did not advance scope revision: %d -> %d %v", beforeRevision, afterRevision, err)
		}
	})
}

func TestNativeLibraryInitializationWitnessesDB(t *testing.T) {
	p, _ := nativeSectionsDB(t)
	r := NewRepository(p)
	id := sectionsLocalLibrary(t, p)
	auth := localSectionOwner(id)
	check := func() error {
		tx, err := p.Begin(t.Context())
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		return r.RequireNativeInitializationWitnessesTx(t.Context(), tx, id)
	}
	if err := check(); err == nil {
		t.Fatal("empty library accepted")
	}
	if err := r.SeedNativeLibraryDefaultsAuthorized(t.Context(), id, auth); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Fatal("missing generated home accepted")
	}
	if err := r.SeedNativeHomeRecentAuthorized(t.Context(), id, "Books", auth); err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatalf("positive ordinary witnesses: %v", err)
	}
	home, err := r.listGeneratedHomeLibraryRecentSections(t.Context(), id)
	if err != nil || len(home) != 2 {
		t.Fatalf("home witnesses: %+v %v", home, err)
	}
	library, err := r.ListByScopeAll(t.Context(), "library", &id)
	if err != nil || len(library) != 5 {
		t.Fatalf("library witnesses: %+v %v", library, err)
	}
	var addedID string
	for _, row := range home {
		if row.SectionType == SectionRecentlyAdded {
			addedID = row.ID
		}
	}
	if addedID == "" {
		t.Fatal("missing actual added witness")
	}
	groupID := catalog.CanonicalUserCollectionsGroupID(id)
	tests := []struct {
		name, sql string
		args      []any
	}{
		{"missing group order revision", "DELETE FROM library_collection_order_revisions WHERE library_id=$1", []any{id}},
		{"disabled library scope", "UPDATE page_sections SET enabled=false WHERE scope='library' AND library_id=$1", []any{id}},
		{"disabled generated kind", "UPDATE page_sections SET enabled=false WHERE id=$1", []any{addedID}},
		{"missing scope revision", "DELETE FROM page_section_scope_revisions WHERE scope='library' AND library_id=$1", []any{id}},
		{"missing home scope revision", "DELETE FROM page_section_scope_revisions WHERE scope='home' AND library_id=0", nil},
		{"missing home section revision", "DELETE FROM page_section_revisions WHERE section_id=$1", []any{addedID}},
		{"missing library section revision", "DELETE FROM page_section_revisions WHERE section_id=$1", []any{library[0].ID}},
		{"missing canonical group", "DELETE FROM library_collection_groups WHERE id=$1", []any{groupID}},
		{"wrong canonical group kind", "UPDATE library_collection_groups SET kind='regular' WHERE id=$1", []any{groupID}},
		{"wrong canonical group label", "UPDATE library_collection_groups SET label='wrong-canonical-label' WHERE id=$1", []any{groupID}},
		{"wrong canonical group ID with positive witnesses", "UPDATE library_collection_groups SET id=id||'_wrong' WHERE id=$1", []any{groupID}},
		{"missing generated kind", "DELETE FROM page_sections WHERE id=$1", []any{addedID}},
		{"wrong generated source", "UPDATE page_sections SET config=jsonb_set(config,'{generated_source}','\"wrong-source\"'::jsonb) WHERE id=$1", []any{addedID}},
		{"missing generated source", "UPDATE page_sections SET config=config-'generated_source' WHERE id=$1", []any{addedID}},
		{"wrong legacy library ID", "UPDATE page_sections SET config=jsonb_set(config,'{filter_library_id}',to_jsonb($2::int)) WHERE id=$1", []any{addedID, id + 1}},
		{"wrong generated library ID despite matching legacy ID", "UPDATE page_sections SET config=jsonb_set(config,'{generated_library_id}',to_jsonb($2::int)) WHERE id=$1", []any{addedID, id + 1}},
		{"zero generated library ID despite matching legacy ID", "UPDATE page_sections SET config=jsonb_set(config,'{generated_library_id}','0'::jsonb) WHERE id=$1", []any{addedID}},
		{"negative legacy library ID", "UPDATE page_sections SET config=jsonb_set(config,'{filter_library_id}','-1'::jsonb) WHERE id=$1", []any{addedID}},
		{"malformed legacy library ID", "UPDATE page_sections SET config=jsonb_set(config,'{filter_library_id}','\"malformed\"'::jsonb) WHERE id=$1", []any{addedID}},
		{"malformed generated library ID despite matching legacy ID", "UPDATE page_sections SET config=jsonb_set(config,'{generated_library_id}','\"malformed\"'::jsonb) WHERE id=$1", []any{addedID}},
		{"malformed generated kind", "UPDATE page_sections SET config=jsonb_set(config,'{generated_kind}','7'::jsonb) WHERE id=$1", []any{addedID}},
		{"conflicting generated kind", "UPDATE page_sections SET config=jsonb_set(config,'{generated_kind}','\"recently_released\"'::jsonb) WHERE id=$1", []any{addedID}},
		{"unknown generated kind", "UPDATE page_sections SET config=jsonb_set(config,'{generated_kind}','\"unknown-kind\"'::jsonb) WHERE id=$1", []any{addedID}},
		{"wrong generated section type", "UPDATE page_sections SET section_type='random' WHERE id=$1", []any{addedID}},
		{"custom filter is not required ebook added kind", "UPDATE page_sections SET section_type='custom_filter' WHERE id=$1", []any{addedID}},
		{"malformed config object", "UPDATE page_sections SET config='[]'::jsonb WHERE id=$1", []any{addedID}},
	}
	// Each real revision table permits nonpositive bigint values. Cover both
	// home and library rows/counters instead of inferring validity from counts.
	for _, revision := range []int64{0, -1} {
		for _, witness := range []struct {
			name, sql string
			key       any
		}{
			{"group order", "UPDATE library_collection_order_revisions SET revision=$2 WHERE library_id=$1", id},
			{"library scope", "UPDATE page_section_scope_revisions SET revision=$2 WHERE scope='library' AND library_id=$1", id},
			{"home scope", "UPDATE page_section_scope_revisions SET revision=$2 WHERE scope='home' AND library_id=$1", 0},
		} {
			tests = append(tests, struct {
				name, sql string
				args      []any
			}{fmt.Sprintf("%s revision %d", witness.name, revision), witness.sql, []any{witness.key, revision}})
		}
		for _, row := range append(append([]*PageSection(nil), library...), home...) {
			tests = append(tests, struct {
				name, sql string
				args      []any
			}{fmt.Sprintf("%s %s section revision %d", row.Scope, row.SectionType, revision),
				"UPDATE page_section_revisions SET revision=$2 WHERE section_id=$1", []any{row.ID, revision}})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			tag, err := tx.Exec(t.Context(), tt.sql, tt.args...)
			if err != nil || tag.RowsAffected() == 0 {
				t.Fatalf("invalid-witness fixture did not change a real row: %v", err)
			}
			if tt.name == "wrong canonical group ID with positive witnesses" {
				var positive bool
				if err := tx.QueryRow(t.Context(), `SELECT
 EXISTS(SELECT 1 FROM library_collection_groups WHERE id=$2||'_wrong' AND library_id=$1 AND kind='user_collections' AND label='user-collections')
 AND EXISTS(SELECT 1 FROM library_collection_order_revisions WHERE library_id=$1 AND revision>0)
 AND (SELECT count(*) FROM page_section_scope_revisions WHERE ((scope='library' AND library_id=$1) OR (scope='home' AND library_id=0)) AND revision>0)=2
 AND (SELECT count(*) FROM page_sections s JOIN page_section_revisions v ON v.section_id=s.id AND v.revision>0
 WHERE (s.scope='library' AND s.library_id=$1) OR (s.scope='home' AND s.id=ANY($3)))=7`, id, groupID, []string{home[0].ID, home[1].ID}).Scan(&positive); err != nil || !positive {
					t.Fatalf("wrong-ID fixture lost otherwise positive witnesses: %v", err)
				}
			}
			var e *catalog.NativeOnboardingError
			err = r.RequireNativeInitializationWitnessesTx(t.Context(), tx, id)
			if !errors.As(err, &e) || e.Code != "initialization_incomplete" {
				t.Fatalf("invalid witnesses accepted or wrong error: %v", err)
			}
		})
	}
	for _, shape := range []string{"legacy filter_library_id", "generated_library_id"} {
		t.Run("positive "+shape+" witnesses", func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			key := "filter_library_id"
			if shape == "generated_library_id" {
				key = "generated_library_id"
			}
			tag, err := tx.Exec(t.Context(), `UPDATE page_sections SET config=jsonb_build_object(
 'generated_source','home_library_recent',$2::text,$3::int) WHERE id=ANY($1)`, []string{home[0].ID, home[1].ID}, key, id)
			if err != nil || tag.RowsAffected() != 2 {
				t.Fatalf("positive metadata fixture: %v", err)
			}
			if err := r.RequireNativeInitializationWitnessesTx(t.Context(), tx, id); err != nil {
				t.Fatalf("valid metadata shape refused: %v", err)
			}
		})
	}
	locks := []struct {
		name, update, lock string
		args               []any
	}{
		{"canonical group", "UPDATE library_collection_groups SET title=title WHERE id=$1", "SELECT id FROM library_collection_groups WHERE id=$1 FOR UPDATE", []any{groupID}},
		{"library collection order revision", "UPDATE library_collection_order_revisions SET revision=revision+1 WHERE library_id=$1", "SELECT library_id FROM library_collection_order_revisions WHERE library_id=$1 FOR UPDATE", []any{id}},
		{"library scope revision", "UPDATE page_section_scope_revisions SET revision=revision+1 WHERE scope='library' AND library_id=$1", "SELECT library_id FROM page_section_scope_revisions WHERE scope='library' AND library_id=$1 FOR UPDATE", []any{id}},
		{"home scope revision", "UPDATE page_section_scope_revisions SET revision=revision+1 WHERE scope='home' AND library_id=$1", "SELECT library_id FROM page_section_scope_revisions WHERE scope='home' AND library_id=$1 FOR UPDATE", []any{0}},
	}
	for _, row := range append(append([]*PageSection(nil), library...), home...) {
		locks = append(locks, struct {
			name, update, lock string
			args               []any
		}{fmt.Sprintf("%s %s section", row.Scope, row.SectionType),
			"UPDATE page_sections SET title=title WHERE id=$1", "SELECT id FROM page_sections WHERE id=$1 FOR UPDATE", []any{row.ID}},
			struct {
				name, update, lock string
				args               []any
			}{fmt.Sprintf("%s %s section revision", row.Scope, row.SectionType),
				"UPDATE page_section_revisions SET revision=revision+1 WHERE section_id=$1", "SELECT section_id FROM page_section_revisions WHERE section_id=$1 FOR UPDATE", []any{row.ID}})
	}
	t.Run("witness locks block concurrent writer", func(t *testing.T) {
		tx, err := p.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err := r.RequireNativeInitializationWitnessesTx(t.Context(), tx, id); err != nil {
			t.Fatal(err)
		}
		for _, tt := range locks {
			t.Run(tt.name, func(t *testing.T) {
				other, err := p.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer other.Rollback(context.Background())
				if _, err = other.Exec(t.Context(), "SET LOCAL lock_timeout='75ms'"); err != nil {
					t.Fatal(err)
				}
				_, err = other.Exec(t.Context(), tt.update, tt.args...)
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "55P03" {
					t.Fatalf("witness writer was not locked: %v", err)
				}
			})
		}
	})
	t.Run("conflicting writer already holds lock before witness NOWAIT", func(t *testing.T) {
		for _, tt := range locks {
			t.Run(tt.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				other, err := p.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Rollback(context.Background())
				// Lock only the named witness, avoiding trigger locks on earlier witnesses.
				var locked any
				if err := other.QueryRow(ctx, tt.lock, tt.args...).Scan(&locked); err != nil {
					t.Fatal(err)
				}
				tx, err := p.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout='500ms'"); err != nil {
					t.Fatal(err)
				}
				err = r.RequireNativeInitializationWitnessesTx(ctx, tx, id)
				var e *catalog.NativeOnboardingError
				var pe *pgconn.PgError
				if !errors.As(err, &e) || e.Code != "native_storage_unavailable" || !errors.As(err, &pe) || pe.Code != "55P03" {
					t.Fatalf("NOWAIT refusal must retain typed error and 55P03 cause: %v", err)
				}
			})
		}
	})
}

func TestNativeLibrarySectionsOriginalWritersDB(t *testing.T) {
	_, writerDSN := nativeSectionsDB(t)
	// Original tests open their own pools from this exact verified clone URL.
	t.Setenv("SILO_TEST_DATABASE_URL", writerDSN)
	for _, tt := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"canonical and CAS", TestSectionMutationCanonicalAndCASDB},
		{"waiting legacy writer", TestSectionMutationLegacyWriterInvalidatesWaitingGuardDB},
		{"opposing orders", TestSectionMutationOpposingMultiScopeOrdersDB},
		{"references and orphan repair", TestSectionMutationReferencesAndOrphanRepairDB},
		{"reset rollback and no-op", TestSectionMutationResetRollbackAndNoopDB},
		{"concurrent create many", TestSectionMutationConcurrentCreateManyPositionsDB},
		{"generated revisions", TestSectionGeneratedWritersAdvanceRevisionsDB},
		{"featured revisions", TestSectionFeaturedWritersAdvanceRevisionsDB},
	} {
		t.Run(tt.name, tt.run)
	}
}
