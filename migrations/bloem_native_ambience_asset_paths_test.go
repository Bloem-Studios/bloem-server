package migrations

import (
	"strings"
	"testing"
)

// Stored artwork paths move to the native route and back; other values,
// including absolute URLs that merely contain the old path, are untouched.
func TestBloemNativeAmbienceAssetPathsPostgres(t *testing.T) {
	tx, schema := adminMigrationFixture(t)
	migrationExec(t, tx, `
CREATE TABLE ambience_packs (id text PRIMARY KEY, assets jsonb NOT NULL DEFAULT '{}'::jsonb);
CREATE TABLE promotions (id text PRIMARY KEY, image_url text NOT NULL, placement jsonb NOT NULL DEFAULT '{}'::jsonb);
INSERT INTO ambience_packs VALUES
  ('legacy', '{"banner_url":"/api/v1/ambience/assets/aaaa.png","sprites":["/api/v1/ambience/assets/bbbb.png","https://cdn.example.test/s.png"]}'),
  ('remote', '{"banner_url":"https://cdn.example.test/api/v1/ambience/assets/cccc.png"}');
INSERT INTO promotions VALUES
  ('legacy', '/api/v1/ambience/assets/dddd.png', '{"playback_style":"pip","video_url":"/api/v1/ambience/assets/eeee.mp4"}'),
  ('remote', 'https://cdn.example.test/card.png', '{}');`)

	read := func() string {
		var out string
		if err := tx.QueryRow(t.Context(), `
SELECT string_agg(v, ' | ' ORDER BY v) FROM (
  SELECT id || ':' || assets::text AS v FROM ambience_packs
  UNION ALL SELECT id || ':' || image_url || ':' || placement::text FROM promotions) rows`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := read()

	const migration = "20261031234831_bloem_native_ambience_asset_paths"
	migrationExec(t, tx, adminMigrationSQL(t, migration, schema, false))
	after := read()
	for _, want := range []string{
		`"banner_url": "/api/bloem/v1/ambience/assets/aaaa.png"`,
		`"/api/bloem/v1/ambience/assets/bbbb.png"`,
		`"https://cdn.example.test/s.png"`,
		`"https://cdn.example.test/api/v1/ambience/assets/cccc.png"`,
		`legacy:/api/bloem/v1/ambience/assets/dddd.png:`,
		`"video_url": "/api/bloem/v1/ambience/assets/eeee.mp4"`,
		`remote:https://cdn.example.test/card.png:`,
	} {
		if !strings.Contains(after, want) {
			t.Errorf("after up: missing %s in %s", want, after)
		}
	}

	migrationExec(t, tx, adminMigrationSQL(t, migration, schema, true))
	if got := read(); got != before {
		t.Fatalf("after down:\n%s\nwant\n%s", got, before)
	}
}
