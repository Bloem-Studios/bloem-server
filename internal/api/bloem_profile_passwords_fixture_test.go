package api

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/config"
)

// enableProfilePasswords turns on the admin switch that lets profiles sign in
// with their own password, which is off by default.
func enableProfilePasswords(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO server_settings (key, value) VALUES ($1, 'true')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, config.ProfilePasswordsEnabledSettingKey); err != nil {
		t.Fatalf("enable profile passwords: %v", err)
	}
}
