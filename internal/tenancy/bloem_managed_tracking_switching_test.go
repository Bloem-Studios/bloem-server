package tenancy_test

// Managed Pastime tracking adds triggers to tables upstream Silo writes
// (users, user_profiles, organization_memberships, user_watch_history,
// watch_provider_connections, watch_provider_scrobble_sessions). While the
// database is mirrored for Silo switching, Silo-shaped writes to each must
// still succeed and keep the managed projection, playback sources and
// credentials consistent.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func grantDefaultOrganization(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner int) (*managedtracking.Service, int) {
	t.Helper()
	// A served deployment's default organization is active with its owner.
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='active', owner_account_id=$1 WHERE is_default`, owner); err != nil {
		t.Fatal(err)
	}
	var install int
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('bloem.pastime','test','fixture') RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	var org string
	if err := pool.QueryRow(ctx, `SELECT public.bloem_default_organization_id()::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte(strings.Repeat("fixture-only-key-", 3)))
	if err != nil {
		t.Fatal(err)
	}
	s := &managedtracking.Service{Pool: pool, Cipher: cipher}
	if err := s.Grant(ctx, install, org); err != nil {
		t.Fatalf("grant default organization: %v", err)
	}
	return s, install
}

func TestMirroredSiloWritesPassManagedTrackingTriggers(t *testing.T) {
	ctx, pool, existing, admin := mirroredPool(t)
	// History Silo wrote before the grant receives a source on grant.
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id)
		VALUES ('mt0-main', $1, 'Main', true, public.bloem_default_organization_id(), (SELECT id FROM access_groups WHERE is_default))`, existing); err != nil {
		t.Fatal(err)
	}
	siloHistory := func(id string, account int, profile string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_watch_history (id, user_id, profile_id, media_item_id, watched_at, duration_seconds, completed, source, watch_identity)
			VALUES ($1, $2, $3, 'mt-media', now(), 100, true, 'playback', '{}')`, id, account, profile); err != nil {
			t.Fatalf("silo history write %s: %v", id, err)
		}
	}
	siloHistory("mt-before-grant", existing, "mt0-main")
	s, install := grantDefaultOrganization(t, ctx, pool, admin)
	sources := func(history string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_managed_playback_sources WHERE history_id=$1`, history).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if sources("mt-before-grant") != 1 {
		t.Fatal("grant did not backfill Silo's earlier history")
	}

	// Silo creates an account and profile; the projection follows at commit.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	account := siloCreateAccountTx(t, ctx, tx, "mt1", 1)
	siloCreateProfileTx(t, ctx, tx, account, "mt1-main")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit silo account and profile under managed triggers: %v", err)
	}
	projected := func(where string) bool {
		t.Helper()
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_managed_profiles WHERE account_id=$1 AND profile_id='mt1-main' AND `+where+`)`, account).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !projected(`active AND account_active AND email=''`) {
		t.Fatal("silo account and profile are missing from the managed projection")
	}

	// Silo renames and disables; both reach the projection.
	if _, err := pool.Exec(ctx, `UPDATE user_profiles SET name = 'Renamed', updated_at = now() WHERE id = 'mt1-main'`); err != nil {
		t.Fatalf("silo profile rename: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET enabled = false WHERE id = $1`, account); err != nil {
		t.Fatalf("silo account disable: %v", err)
	}
	if !projected(`name='Renamed' AND NOT account_active`) {
		t.Fatal("silo rename or disable did not reach the projection")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET enabled = true WHERE id = $1`, account); err != nil {
		t.Fatal(err)
	}

	// Silo's history writes record sources for the granted tenant.
	siloHistory("mt-after-grant", account, "mt1-main")
	if sources("mt-after-grant") != 1 {
		t.Fatal("silo history write recorded no playback source")
	}
	if _, err := pool.Exec(ctx, `UPDATE user_watch_history SET watched_at = now() WHERE id = 'mt-after-grant'`); err != nil {
		t.Fatalf("silo history update: %v", err)
	}
	if sources("mt-after-grant") != 1 {
		t.Fatal("an unrelated history update recorded another source")
	}

	// A managed connection keeps its credential through Silo's generic upsert,
	// and Silo's scrobble session writes record live sources.
	key := watchsync.PluginProviderKey(install, "bloem.pastime", "pastime", false)
	var connection string
	if err := pool.QueryRow(ctx, `
		INSERT INTO watch_provider_connections (provider, user_id, profile_id, access_token, scrobble_enabled)
		VALUES ($1, $2, 'mt1-main', 'managed-ciphertext', true) RETURNING id::text`, key, account).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_provider_connections (provider, user_id, profile_id, access_token, last_error)
		VALUES ($1, $2, 'mt1-main', 'stale-ciphertext', 'sync finished')
		ON CONFLICT (provider, user_id, profile_id) DO UPDATE SET
			access_token = EXCLUDED.access_token, last_error = EXCLUDED.last_error`, key, account); err != nil {
		t.Fatalf("silo connection upsert: %v", err)
	}
	var token, lastError string
	if err := pool.QueryRow(ctx, `SELECT access_token, last_error FROM watch_provider_connections WHERE id = $1::uuid`, connection).Scan(&token, &lastError); err != nil {
		t.Fatal(err)
	}
	if token != "managed-ciphertext" || lastError != "sync finished" {
		t.Fatalf("silo upsert: token kept=%v last_error=%q", token == "managed-ciphertext", lastError)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_provider_scrobble_sessions (playback_session_id, connection_id, media_item_id, last_progress, duration_seconds, last_action)
		VALUES ('mt-session', $1::uuid, 'mt-media', 0, 100, 'start')`, connection); err != nil {
		t.Fatalf("silo scrobble start: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE watch_provider_scrobble_sessions SET last_action = 'stop', last_progress = 50, updated_at = now()
		WHERE playback_session_id = 'mt-session'`); err != nil {
		t.Fatalf("silo scrobble stop: %v", err)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_managed_playback_sources WHERE session_id = 'mt-session'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 2 {
		t.Fatalf("silo scrobble writes recorded %d live sources, want 2", live)
	}

	// Silo deletes the account; every cascade passes the triggers.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, account); err != nil {
		t.Fatalf("silo account delete under managed triggers: %v", err)
	}
	if projected(`active`) {
		t.Fatal("deleted silo account stays active in the projection")
	}
	if _, err := s.Describe(ctx, install); err != nil {
		t.Fatalf("managed authority after silo writes: %v", err)
	}
}
