package tenancy_test

// The Pastime removal migration drops every trigger, function, table and
// sequence managed tracking added, including the triggers on tables upstream
// Silo writes, and strips list markers from collections. It writes no
// watch-provider connection or child row: the connections enrollment wrote
// stay as ordinary watch-sync rows for the plugin installation that owns the
// provider key. While the database is mirrored for Silo switching, Silo-shaped
// writes to every formerly triggered table must succeed afterwards, including
// deleting an account that has ebook and audiobook progress. Behind a
// long-running lock holder it gives up and rolls back whole rather than queue.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/testutil/migrationfixture"
	"github.com/Silo-Server/silo-server/migrations"
)

const pastimeRemovalVersion int64 = 20261013231500

func pastimeRemovalExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func pastimeRemovalCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// pastimeObjects counts triggers, functions and relations (tables, indexes,
// sequences) and types that managed tracking created.
func pastimeObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	const names = `'^_?(bloem_managed|bloem_companion|bloem_list)'`
	return map[string]int{
		"triggers": pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM pg_trigger t JOIN pg_proc f ON f.oid=t.tgfoid
			WHERE t.tgname ~ `+names+` OR f.proname ~ `+names),
		"functions": pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM pg_proc WHERE proname ~ `+names),
		"relations": pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM pg_class WHERE relname ~ `+names),
		"types":     pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM pg_type WHERE typname ~ `+names),
	}
}

func assertNoPastimeObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for kind, n := range pastimeObjects(t, ctx, pool) {
		if n != 0 {
			t.Errorf("%d managed-tracking %s remain", n, kind)
		}
	}
}

// pastimeRemovalBaseline migrates a database to the version before the
// removal, so the historical managed-tracking schema exists.
func pastimeRemovalBaseline(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool, _ := buildTenantPoolWithMigrations(t, migrationfixture.Before(t, migrations.FS, pastimeRemovalVersion))
	if objects := pastimeObjects(t, ctx, pool); objects["triggers"] != 17 || objects["functions"] != 14 {
		t.Fatalf("baseline lacks the managed-tracking schema: %v", objects)
	}
	return ctx, pool
}

// seedPastimeGrant grants a bloem.pastime installation the default
// organization and enrolls one profile, as managed tracking did.
func seedPastimeGrant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, account int, profile string) (install int, provider string) {
	t.Helper()
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations (plugin_id, version, install_path)
		VALUES ('bloem.pastime', 'fixture', 'fixture') RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO bloem_managed_grants (installation_id, tenant_id)
		VALUES ($1, public.bloem_default_organization_id())`, install)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO bloem_managed_enrollments
		(installation_id, account_id, profile_id, enrollment_id, generation, destination_account_id, destination_profile_id, payload_digest, active)
		VALUES ($1, $2, $3, 'fixture-enrollment', 1, 'destination-account', 'destination-profile', '\x00', true)`, install, account, profile)
	return install, fmt.Sprintf("plugin:%d:pastime", install)
}

func pastimeConnection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, provider string, account int, profile, token string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO watch_provider_connections (provider, user_id, profile_id, access_token, scrobble_enabled)
		VALUES ($1, $2, $3, $4, true) RETURNING id::text`, provider, account, profile, token).Scan(&id); err != nil {
		t.Fatalf("insert %s connection: %v", provider, err)
	}
	return id
}

// watchSyncRows fingerprints watch_provider_connections and every table with a
// foreign key onto it: a row count and an md5 over the sorted row texts.
func watchSyncRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT 'watch_provider_connections'
		UNION SELECT conrelid::regclass::text FROM pg_constraint
		WHERE contype = 'f' AND confrelid = 'public.watch_provider_connections'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"watch_provider_sync_runs", "watch_provider_history_exports", "watch_provider_list_items",
		"watch_provider_scrobble_sessions", "watch_provider_rating_items", "watch_provider_dropped_items"} {
		if !slices.Contains(tables, child) {
			t.Fatalf("%s no longer references watch_provider_connections: %v", child, tables)
		}
	}
	fingerprint := make(map[string]string, len(tables))
	for _, table := range tables {
		var count int
		var digest string
		if err := pool.QueryRow(ctx, `SELECT count(*), coalesce(md5(string_agg(r::text, E'\n' ORDER BY r::text)), '')
			FROM `+table+` r`).Scan(&count, &digest); err != nil { // regclass text is quoted as needed
			t.Fatalf("fingerprint %s: %v", table, err)
		}
		fingerprint[table] = fmt.Sprintf("%d %s", count, digest)
	}
	return fingerprint
}

func TestPastimeRemovalLeavesNoManagedObjects(t *testing.T) {
	ctx, pool, _ := buildTenantPool(t)
	assertNoPastimeObjects(t, ctx, pool)
}

func TestPastimeRemovalKeepsSiloDataAndWritesInMirroredPhase(t *testing.T) {
	ctx, pool := pastimeRemovalBaseline(t)
	user := siloAccount(t, ctx, pool, "pr-user", "user")
	admin := siloAccount(t, ctx, pool, "pr-admin", "admin")
	pastimeRemovalExec(t, ctx, pool, `UPDATE organizations SET status='active', owner_account_id=$1 WHERE is_default`, admin)
	enterMirrored(t, ctx, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	siloCreateProfileTx(t, ctx, tx, user, "pr-main")
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profiles (
			id, user_id, name, avatar, pin_hash, is_child, is_primary, max_content_rating, max_advisory_age,
			require_advisory_age, quality_preference, language, preferred_metadata_language, subtitle_language,
			subtitle_mode, auto_skip_intro, auto_skip_credits, auto_skip_recap, auto_play_next_preview,
			library_restrictions_enabled, show_forced_subtitles, max_playback_quality, created_at, updated_at
		) VALUES ('pr-kids', $1, 'Kids', '', '', false, false, '', NULL, false,
		          '', '', '', '', '', false, false, false, true, false, false, '', now(), now())`, user); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Silo's own data for the enrolled profile.
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO media_folders (id, type, name) VALUES (900, 'books', 'fixture books')`)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO media_files (id, media_folder_id, file_path) VALUES (900, 900, '/fixture/book.epub')`)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO media_items (content_id, type, title)
		VALUES ('pr-ebook', 'ebook', 'Ebook'), ('pr-audiobook', 'audiobook', 'Audiobook'), ('pr-movie', 'movie', 'Movie')`)
	for _, statement := range []string{
		`INSERT INTO ebook_reader_progress (user_id, profile_id, content_id, file_id, location) VALUES ($1, 'pr-main', 'pr-ebook', 900, 'epubcfi(/6/2)')`,
		`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds, duration_seconds) VALUES ($1, 'pr-main', 'pr-audiobook', 42, 120)`,
		`INSERT INTO user_favorites (user_id, profile_id, media_item_id) VALUES ($1, 'pr-main', 'pr-movie')`,
		`INSERT INTO user_watchlist (user_id, profile_id, media_item_id) VALUES ($1, 'pr-main', 'pr-movie')`,
		`INSERT INTO user_ratings (user_id, profile_id, media_item_id, rating) VALUES ($1, 'pr-main', 'pr-movie', 4)`,
		`INSERT INTO user_watch_history (id, user_id, profile_id, media_item_id, watched_at, duration_seconds, completed, source, watch_identity)
		 VALUES ('pr-history', $1, 'pr-main', 'pr-movie', now(), 100, true, 'playback', '{}')`,
	} {
		pastimeRemovalExec(t, ctx, pool, statement, user)
	}

	// The enrolled profile's managed connection, a profile's own Pastime link
	// and another provider's connection, each with Silo's child rows.
	install, provider := seedPastimeGrant(t, ctx, pool, user, "pr-main")
	managed := pastimeConnection(t, ctx, pool, provider, user, "pr-main", "managed-ciphertext")
	selfLinked := pastimeConnection(t, ctx, pool, provider, user, "pr-kids", "self-linked-ciphertext")
	other := pastimeConnection(t, ctx, pool, "simkl", user, "pr-main", "simkl-ciphertext")
	for _, connection := range []string{managed, other} {
		pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_sync_runs (connection_id, trigger, status, provider)
			SELECT id, 'manual', 'success', provider FROM watch_provider_connections WHERE id=$1::uuid`, connection)
		pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_list_items (connection_id, media_item_id, provider_item_key, kind)
			VALUES ($1::uuid, 'pr-movie', 'fixture-key', 'watchlist')`, connection)
	}
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_scrobble_sessions (playback_session_id, connection_id, media_item_id)
		VALUES ('pr-session', $1::uuid, 'pr-movie')`, managed)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_history_exports (connection_id, history_id, media_item_id, watched_at, provider_item_key, status, last_error)
		VALUES ($1::uuid, 'pr-history', 'pr-movie', now(), 'fixture-key', 'pending', ''),
		       ($1::uuid, 'pr-history-unmatched', 'pr-movie', now(), 'fixture-key', 'not_found', 'not found')`, managed)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_rating_items (connection_id, media_item_id, kind, synced_rating)
		VALUES ($1::uuid, 'pr-movie', 'movie', 4)`, managed)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_dropped_items (connection_id, series_id, provider_item_key, remote_seen)
		VALUES ($1::uuid, 'pr-series', 'fixture-series', true)`, managed)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_history_exports (connection_id, history_id, media_item_id, watched_at, provider_item_key, status)
		VALUES ($1::uuid, 'pr-history', 'pr-movie', now(), 'fixture-key', 'pending')`, selfLinked)

	// Published list collections and one synced collection with no marker.
	const binding = "00000000-0000-0000-0000-0000000000b1"
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO user_personal_collections (id, user_id, profile_id, name, native, collection_type, source_config) VALUES
		('pr-list', $1, 'pr-main', 'Pastime list', true, 'synced', jsonb_build_object('bloem_list_binding', $2::text)),
		('pr-synced', $1, 'pr-main', 'Other synced', true, 'synced', '{"source":"elsewhere"}')`, user, binding)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO library_collections (id, library_id, slug, title, collection_type, source_config)
		VALUES ('pr-server-list', 900, 'pr-server-list', 'Pastime server list', 'manual', '{"bloem_list_binding":"00000000-0000-0000-0000-0000000000b2"}')`)
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO bloem_list_bindings
		(id, installation_id, user_id, profile_id, subject, preview_id, create_digest, source_id, signing_key, grant_cipher, scope, target_id)
		VALUES ($1::uuid, $2, $3, 'pr-main', '{}', gen_random_uuid(), decode(repeat('00', 32), 'hex'), 'source', repeat('A', 43), 'fixture', 'personal', 'pr-list')`,
		binding, install, user)

	watchSyncBefore := watchSyncRows(t, ctx, pool)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("apply the removal in the mirrored phase: %v", err)
	}
	assertNoPastimeObjects(t, ctx, pool)

	// Every watch-provider connection and child row is exactly as it was.
	if after := watchSyncRows(t, ctx, pool); !maps.Equal(after, watchSyncBefore) {
		t.Errorf("watch-sync rows changed:\nbefore %v\nafter  %v", watchSyncBefore, after)
	}
	connections := func(id string) int {
		t.Helper()
		return pastimeRemovalCount(t, ctx, pool, `SELECT
			(SELECT count(*) FROM watch_provider_connections WHERE id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_sync_runs WHERE connection_id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_list_items WHERE connection_id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_scrobble_sessions WHERE connection_id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_history_exports WHERE connection_id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_rating_items WHERE connection_id=$1::uuid)
			+ (SELECT count(*) FROM watch_provider_dropped_items WHERE connection_id=$1::uuid)`, id)
	}
	for _, want := range []struct {
		name, id string
		rows     int
	}{{"enrolled Pastime", managed, 8}, {"self-linked Pastime", selfLinked, 2}, {"other provider", other, 3}} {
		if n := connections(want.id); n != want.rows {
			t.Errorf("%s connection rows = %d, want %d", want.name, n, want.rows)
		}
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM watch_provider_history_exports
		WHERE connection_id=$1::uuid AND status='pending'`, managed); n != 1 {
		t.Errorf("enrolled connection's pending exports = %d, want 1", n)
	}
	var kind, config string
	if err := pool.QueryRow(ctx, `SELECT collection_type, source_config::text FROM user_personal_collections WHERE id='pr-list'`).Scan(&kind, &config); err != nil {
		t.Fatal(err)
	}
	if kind != "manual" || config != "{}" {
		t.Errorf("published personal list = %s %s, want manual {}", kind, config)
	}
	if err := pool.QueryRow(ctx, `SELECT collection_type, source_config->>'source' FROM user_personal_collections WHERE id='pr-synced'`).Scan(&kind, &config); err != nil {
		t.Fatal(err)
	}
	if kind != "synced" || config != "elsewhere" {
		t.Errorf("unmarked synced collection changed: %s %s", kind, config)
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM library_collections WHERE id='pr-server-list' AND source_config='{}'`); n != 1 {
		t.Error("server list marker remains")
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT
		(SELECT count(*) FROM ebook_reader_progress WHERE user_id=$1 AND location='epubcfi(/6/2)')
		+ (SELECT count(*) FROM user_watch_progress WHERE user_id=$1 AND position_seconds=42)
		+ (SELECT count(*) FROM user_favorites WHERE user_id=$1)
		+ (SELECT count(*) FROM user_watchlist WHERE user_id=$1)
		+ (SELECT count(*) FROM user_ratings WHERE user_id=$1 AND rating=4)
		+ (SELECT count(*) FROM user_watch_history WHERE user_id=$1)`, user); n != 6 {
		t.Errorf("Silo user data rows = %d, want 6", n)
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM plugin_installations WHERE id=$1`, install); n != 1 {
		t.Error("the removal deleted the plugin installation that owns the kept connections")
	}

	// Silo-shaped writes to every formerly triggered table.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "pr-new", 1)
	siloCreateProfileTx(t, ctx, tx, account, "pr-new-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit silo account and profile: %v", err)
	}
	for _, statement := range []string{
		`UPDATE users SET username='pr-new-renamed', enabled=false WHERE id=$1`,
		`UPDATE users SET enabled=true WHERE id=$1`,
		`UPDATE user_profiles SET name='Renamed', updated_at=now() WHERE user_id=$1`,
		`INSERT INTO user_watch_history (id, user_id, profile_id, media_item_id, watched_at, duration_seconds, completed, source, watch_identity)
		 VALUES ('pr-new-history', $1, 'pr-new-main', 'pr-movie', now(), 100, false, 'playback', '{}')`,
		`UPDATE user_watch_history SET completed=true WHERE user_id=$1`,
		`INSERT INTO user_watch_progress (user_id, profile_id, media_item_id, position_seconds, duration_seconds) VALUES ($1, 'pr-new-main', 'pr-audiobook', 10, 120)`,
		`UPDATE user_watch_progress SET position_seconds=20 WHERE user_id=$1`,
		`INSERT INTO ebook_reader_progress (user_id, profile_id, content_id, file_id, location) VALUES ($1, 'pr-new-main', 'pr-ebook', 900, 'epubcfi(/6/4)')`,
		`UPDATE ebook_reader_progress SET location='epubcfi(/6/8)' WHERE user_id=$1`,
		`INSERT INTO user_favorites (user_id, profile_id, media_item_id) VALUES ($1, 'pr-new-main', 'pr-movie')`,
		`INSERT INTO user_watchlist (user_id, profile_id, media_item_id) VALUES ($1, 'pr-new-main', 'pr-movie')`,
		`INSERT INTO user_ratings (user_id, profile_id, media_item_id, rating) VALUES ($1, 'pr-new-main', 'pr-movie', 3)`,
		`UPDATE user_ratings SET rating=5 WHERE user_id=$1`,
		`DELETE FROM user_watchlist WHERE user_id=$1`,
	} {
		pastimeRemovalExec(t, ctx, pool, statement, account)
	}

	// A plugin:N:pastime key is an ordinary watch-sync connection now: Silo's
	// upsert and update write its credential.
	fresh := pastimeConnection(t, ctx, pool, provider, account, "pr-new-main", "first")
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_connections (provider, user_id, profile_id, access_token, last_error)
		VALUES ($1, $2, 'pr-new-main', 'second', 'sync finished')
		ON CONFLICT (provider, user_id, profile_id) DO UPDATE SET access_token=EXCLUDED.access_token, last_error=EXCLUDED.last_error`, provider, account)
	pastimeRemovalExec(t, ctx, pool, `UPDATE watch_provider_connections SET access_token='rotated' WHERE id IN ($1::uuid, $2::uuid)`, selfLinked, managed)
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM watch_provider_connections
		WHERE (id=$1::uuid AND access_token='second' AND last_error='sync finished') OR (id IN ($2::uuid, $3::uuid) AND access_token='rotated')`,
		fresh, selfLinked, managed); n != 3 {
		t.Errorf("silo credential writes kept = %d, want 3", n)
	}
	// The kept enrolled connection is the user's: Silo's exporter drains it and
	// disconnecting removes it with its child rows.
	pastimeRemovalExec(t, ctx, pool, `UPDATE watch_provider_history_exports SET status='sent', attempt_count=1, last_attempt_at=now()
		WHERE connection_id=$1::uuid AND status='pending'`, managed)
	pastimeRemovalExec(t, ctx, pool, `DELETE FROM watch_provider_connections WHERE id=$1::uuid`, managed)
	if n := connections(managed); n != 0 {
		t.Errorf("disconnected enrolled connection rows = %d, want 0", n)
	}
	pastimeRemovalExec(t, ctx, pool, `INSERT INTO watch_provider_scrobble_sessions (playback_session_id, connection_id, media_item_id)
		VALUES ('pr-new-session', $1::uuid, 'pr-movie')`, fresh)
	pastimeRemovalExec(t, ctx, pool, `UPDATE watch_provider_scrobble_sessions SET last_progress=50, last_action='pause' WHERE connection_id=$1::uuid`, fresh)

	var oidc, identity int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations (plugin_id, version, install_path, enabled)
		VALUES ('silo.auth.oidc', 'fixture', 'fixture-oidc', true) RETURNING id`).Scan(&oidc); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_auth_identities (plugin_installation_id, external_subject, user_id)
		VALUES ($1, 'https://issuer.example|subject', $2) RETURNING id`, oidc, account).Scan(&identity); err != nil {
		t.Fatal(err)
	}
	pastimeRemovalExec(t, ctx, pool, `UPDATE plugin_auth_identities SET issuer='https://issuer.example', external_subject='https://issuer.example|other' WHERE id=$1`, identity)
	pastimeRemovalExec(t, ctx, pool, `UPDATE plugin_installations SET enabled=false WHERE id=$1`, oidc)
	pastimeRemovalExec(t, ctx, pool, `DELETE FROM plugin_auth_identities WHERE id=$1`, identity)

	// Deleting an account with ebook and audiobook progress failed while the
	// companion book trigger journaled the cascade.
	for _, id := range []int{account, user} {
		pastimeRemovalExec(t, ctx, pool, `DELETE FROM users WHERE id=$1`, id)
		if n := pastimeRemovalCount(t, ctx, pool, `SELECT
			(SELECT count(*) FROM ebook_reader_progress WHERE user_id=$1)
			+ (SELECT count(*) FROM user_watch_progress WHERE user_id=$1)
			+ (SELECT count(*) FROM user_profiles WHERE user_id=$1)
			+ (SELECT count(*) FROM watch_provider_connections WHERE user_id=$1)
			+ (SELECT count(*) FROM organization_memberships WHERE account_id=$1)`, id); n != 0 {
			t.Errorf("account %d rows left after delete: %d", id, n)
		}
	}
}

func TestPastimeRemovalFailsAtomicallyOnUnknownDependency(t *testing.T) {
	ctx, pool := pastimeRemovalBaseline(t)
	user := siloAccount(t, ctx, pool, "pr-atomic", "user")
	pastimeRemovalExec(t, ctx, pool, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id)
		VALUES ('pr-atomic-main', $1, 'Main', true, public.bloem_default_organization_id(), (SELECT id FROM access_groups WHERE is_default))`, user)
	_, provider := seedPastimeGrant(t, ctx, pool, user, "pr-atomic-main")
	managed := pastimeConnection(t, ctx, pool, provider, user, "pr-atomic-main", "managed-ciphertext")
	pastimeRemovalExec(t, ctx, pool, `CREATE VIEW pastime_unknown_dependency AS SELECT * FROM bloem_managed_grants`)

	before := pastimeObjects(t, ctx, pool)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err == nil {
		t.Fatal("an unknown dependency was silently dropped")
	}
	after := pastimeObjects(t, ctx, pool)
	for kind, n := range before {
		if after[kind] != n {
			t.Errorf("failed removal changed %s: %d -> %d", kind, n, after[kind])
		}
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM watch_provider_connections WHERE id=$1::uuid`, managed); n != 1 {
		t.Error("failed removal lost the managed connection")
	}
}

// A long-running transaction on a formerly triggered table must make the
// removal give up and roll back whole, not hold users locked while it queues.
func TestPastimeRemovalGivesUpBehindALongLockHolder(t *testing.T) {
	ctx, pool := pastimeRemovalBaseline(t)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `LOCK TABLE user_watch_progress IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}

	before := pastimeObjects(t, ctx, pool)
	err = database.RunMigrations(ctx, pool, migrations.FS, "sql")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" { // lock_not_available
		t.Fatalf("removal behind a held lock = %v, want lock_not_available", err)
	}
	after := pastimeObjects(t, ctx, pool)
	for kind, n := range before {
		if after[kind] != n {
			t.Errorf("timed-out removal changed %s: %d -> %d", kind, n, after[kind])
		}
	}
	if n := pastimeRemovalCount(t, ctx, pool, `SELECT count(*) FROM goose_db_version WHERE version_id=$1`, pastimeRemovalVersion); n != 0 {
		t.Error("timed-out removal was recorded as applied")
	}

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("retry after the lock holder finished: %v", err)
	}
	assertNoPastimeObjects(t, ctx, pool)
}
