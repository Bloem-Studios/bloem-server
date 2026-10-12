package bridgeimport

import (
	"database/sql"
	"net/url"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Fork tests can supply expected metadata for fields absent from schema22.
// The source profile and the whole target profile are still compared below.
var completeExpectedImportedProfile = func(*testing.T, *pgxpool.Pool, importFixture, *userstore.Profile) {}

func assertImportedStoreReads(t *testing.T, pool *pgxpool.Pool, fixture importFixture) {
	t.Helper()
	uri := url.URL{Scheme: sourceFileScheme, Path: fixture.path, RawQuery: sourceReadOnlyQuery}
	source, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close() //nolint:errcheck
	sqlite := userdb.NewSQLiteUserStore(source)
	pg, err := pgstore.NewPostgresProvider(pool).ForUser(t.Context(), fixture.identity.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"parent", "child"} {
		a := readSchema22Profile(t, source, id)
		completeExpectedImportedProfile(t, pool, fixture, a)
		b, err := pg.GetProfile(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatal("profile semantics changed")
		}
	}
	a, err := sqlite.GetAudioPreference(t.Context(), "parent", fixture.item)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pg.GetAudioPreference(t.Context(), "parent", fixture.item)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || b.AudioLanguage != "en" {
		t.Fatal("legacy track preference changed")
	}
	identity := userstore.SettingIdentity{Key: "playback.audio_language", Scope: settingscontract.ScopeProfile, ProfileID: "parent"}
	canonical, err := pg.GetSettingValue(t.Context(), identity)
	if err != nil || canonical == nil || string(canonical.Value) != `"fr"` || canonical.Revision != 8 {
		t.Fatal("canonical preference lost to legacy value")
	}
	history, err := pg.ListHistory(t.Context(), "parent", 20, 0)
	if err != nil || len(history) != 1 || history[0].ID != "history" {
		t.Fatal("history identity or visibility changed")
	}
	overrides, err := pg.ListSectionOverrides(t.Context(), "child", "home", "")
	if err != nil || len(overrides) != 1 || overrides[0].ID != "override" || !overrides[0].Removed {
		t.Fatal("section override semantics changed")
	}
	opaque, err := pg.GetSetting(t.Context(), "opaque")
	if err != nil || opaque != "synthetic opaque value" {
		t.Fatal("opaque setting changed")
	}
}

func pgstoreForTest(t *testing.T, pool *pgxpool.Pool, accountID int) userstore.UserStore {
	t.Helper()
	store, err := pgstore.NewPostgresProvider(pool).ForUser(t.Context(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// Compare historical source semantics without querying columns introduced
// after schema22. New target fields remain zero in the expected profile.
func readSchema22Profile(t *testing.T, source *sql.DB, id string) *userstore.Profile {
	t.Helper()
	var p userstore.Profile
	if err := source.QueryRowContext(t.Context(), `
 SELECT id, name, avatar, pin_hash, is_child, is_primary, max_content_rating,
  quality_preference, language, subtitle_language, subtitle_mode,
  auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview,
  show_forced_subtitles, library_restrictions_enabled, max_playback_quality,
  created_at, updated_at FROM profiles WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.Avatar, &p.PINHash, &p.IsChild, &p.IsPrimary, &p.MaxContentRating,
		&p.QualityPreference, &p.Language, &p.SubtitleLanguage, &p.SubtitleMode,
		&p.AutoSkipIntro, &p.AutoSkipCredits, &p.AutoSkipRecap, &p.AutoPlayNextPreview,
		&p.ShowForcedSubtitles, &p.LibraryRestrictionsEnabled, &p.MaxPlaybackQuality,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		t.Fatal(err)
	}
	rows, err := source.QueryContext(t.Context(), `SELECT library_id FROM profile_allowed_libraries WHERE profile_id = ? ORDER BY library_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var libraryID int
		if err := rows.Scan(&libraryID); err != nil {
			t.Fatal(err)
		}
		p.AllowedLibraryIDs = append(p.AllowedLibraryIDs, libraryID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return &p
}
