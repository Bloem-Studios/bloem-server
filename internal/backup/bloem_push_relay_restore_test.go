package backup

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// Bloem's push relay credential is this server's, as Silo's is: a restore
// classifies it server-specific, a full restore takes it from the live server
// as one unit, and its key is a secret.
func TestBloemPushRelayCredentialIsThisServers(t *testing.T) {
	keys := notifications.BloemRelaySettingKeys()
	if len(keys) != 6 {
		t.Fatalf("Bloem relay keys = %v", keys)
	}
	for _, key := range keys {
		if kind, ok := SettingKindOf(key); !ok || kind != SettingKindServerSpecific {
			t.Errorf("%s: kind %q (%v), want server-specific", key, kind, ok)
		}
	}
	var bundle *outboundBundle
	for i, b := range outboundBundles {
		if slices.Contains(b.keys, notifications.BloemSettingPushRelayAPIKey) {
			bundle = &outboundBundles[i]
		}
	}
	if bundle == nil {
		t.Fatal("no outbound bundle carries Bloem's push relay credential")
	}
	for _, key := range keys {
		if !slices.Contains(bundle.keys, key) {
			t.Errorf("Bloem's push relay bundle leaves out %s", key)
		}
		if slices.ContainsFunc(outboundBundles, func(b outboundBundle) bool {
			return b.name != bundle.name && slices.Contains(b.keys, key)
		}) {
			t.Errorf("%s is in two outbound bundles", key)
		}
	}
	if !slices.Equal(bundle.secrets, []string{notifications.BloemSettingPushRelayAPIKey}) {
		t.Errorf("Bloem's push relay bundle secrets = %v", bundle.secrets)
	}
	if !slices.Contains(restoredSecretSettings, notifications.BloemSettingPushRelayAPIKey) {
		t.Error("Bloem's push relay key is not a restored secret setting")
	}
}

// A full restore keeps the live server's credentials for both relays, and a
// restore with no live server clears both keys.
func TestBloemRestoreKeepsBothRelayCredentialsDB(t *testing.T) {
	pool, dsn := testDB(t)
	ctx := t.Context()
	into := createDatabase(t, pool, dsn)
	restored, err := pgxpool.New(ctx, into)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	if _, err := restored.Exec(ctx, `CREATE TABLE server_settings (key text PRIMARY KEY, value text NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}

	silo := []string{notifications.SettingPushRelayURL, notifications.SettingPushRelayDeploymentID, notifications.SettingPushRelayAPIKey,
		notifications.SettingPushRelayExpiresAt, notifications.SettingPushRelayKeyPrefix, notifications.SettingPushRelayReregister}
	bloem := notifications.BloemRelaySettingKeys()
	all := append(slices.Clone(silo), bloem...)
	var saved [][2]string
	rows, err := pool.Query(ctx, `SELECT key, value FROM server_settings WHERE key = ANY($1)`, all)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kv [2]string
		if err := rows.Scan(&kv[0], &kv[1]); err != nil {
			t.Fatal(err)
		}
		saved = append(saved, kv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM server_settings WHERE key = ANY($1)`, all)
		for _, kv := range saved {
			_, _ = pool.Exec(context.Background(), `INSERT INTO server_settings (key, value) VALUES ($1, $2)`, kv[0], kv[1])
		}
	})

	put := func(exec func(context.Context, string, ...any) error, keys []string, prefix string) {
		t.Helper()
		for _, key := range keys {
			if err := exec(ctx, `INSERT INTO server_settings (key, value) VALUES ($1, $2)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, prefix+key); err != nil {
				t.Fatal(err)
			}
		}
	}
	livePut := func(ctx context.Context, sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	backupPut := func(ctx context.Context, sql string, args ...any) error {
		_, err := restored.Exec(ctx, sql, args...)
		return err
	}
	put(livePut, all, "live:")
	put(backupPut, all, "backup:")

	read := func(key string) string {
		t.Helper()
		var v string
		if err := restored.QueryRow(ctx, `SELECT coalesce((SELECT value FROM server_settings WHERE key = $1), '')`, key).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	loader := PGLoader{}
	carried, _, err := loader.CarryOutboundSettings(ctx, into, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(carried, "the push relay") || !slices.Contains(carried, "Bloem's push relay") {
		t.Fatalf("carried %v, want both relays", carried)
	}
	for _, key := range all {
		if got := read(key); got != "live:"+key {
			t.Errorf("%s = %q after a restore, want the live server's", key, got)
		}
	}

	put(backupPut, all, "backup:")
	if _, cleared, err := loader.CarryOutboundSettings(ctx, into, "", nil); err != nil || !slices.Contains(cleared, "Bloem's push relay") {
		t.Fatalf("carrying none: %v, cleared %v", err, cleared)
	}
	if read(notifications.BloemSettingPushRelayAPIKey) != "" || read(notifications.SettingPushRelayAPIKey) != "" {
		t.Fatal("a restore with no live server kept a relay key from the backup")
	}
}
