package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the real account/session/tenant repositories on isolated temporary
// tables. The viewer is the context-sensitive fixture, not a live PDP/server.
func TestBloemEventsSocketPostgresAuthorityBinding(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"users", "organizations", "organization_memberships", "auth_sessions", "user_profiles"} {
		name := pgx.Identifier{table}.Sanitize()
		if _, err := pool.Exec(t.Context(), "CREATE TEMP TABLE "+name+" (LIKE public."+name+" INCLUDING DEFAULTS) ON COMMIT PRESERVE ROWS"); err != nil {
			t.Fatal(err)
		}
	}
	f := newBloemSocketFixture(t)
	organization := f.tenants.tenant.OrganizationID
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (id, email, username, password_hash, role, account_incarnation_id) VALUES (7, 'socket@example.test', 'socket', 'unused', 'user', $1)`, f.users.user.AccountIncarnationID)
	exec(`INSERT INTO organizations (id, slug, name, status, owner_account_id, policy_revision) VALUES ($1, 'socket', 'Socket', 'active', 7, 2)`, organization)
	exec(`INSERT INTO organization_memberships (id, organization_id, account_id, status, legacy_role, security_revision) VALUES ($1, $2, 7, 'active', 'user', 3)`, f.tenants.tenant.MembershipID, organization)
	exec(`INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id) VALUES ('profile', 7, 'Viewer', $1, 1)`, organization)
	sessions := auth.NewSessionRepository(pool)
	sessionID := uuid.NewString()
	if err := sessions.Create(t.Context(), models.AuthSession{ID: sessionID, UserID: 7, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.claims.SessionID, f.identity.SessionID = sessionID, sessionID
	f.identity.ProfileID, f.identity.ProfileToken = "profile", "pin-proof"
	f.ctx = apimw.SetProfileID(f.ctx, "profile")
	store := tenancy.NewStore(pool)
	h := NewBloemEventsSocketV2(f.h.Events, evt.NewSocketTicketStore(nil), sessions, auth.NewUserRepository(pool), f.viewer, func(context.Context, int, string) (bool, bool, error) { return true, true, nil }, tenancy.NewResolver(store), store, "")
	ticket, err := h.Mint(f.ctx, f.identity)
	if err != nil {
		t.Fatalf("mint with real authority: %v", err)
	}
	proof, err := h.Tickets.Consume(t.Context(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.Validate(t.Context(), proof); err != nil {
		t.Fatalf("contextless validation: %v", err)
	}
	for _, test := range []struct{ name, change, restore string }{
		{"profile moved with unchanged scope", `UPDATE user_profiles SET organization_id = gen_random_uuid()`, `UPDATE user_profiles SET organization_id = (SELECT id FROM organizations)`},
		{"profile transferred to another account", `UPDATE user_profiles SET user_id = 8`, `UPDATE user_profiles SET user_id = 7`},
		{"membership revoked", `UPDATE organization_memberships SET status = 'suspended'`, `UPDATE organization_memberships SET status = 'active'`},
		{"membership revision", `UPDATE organization_memberships SET security_revision = 4`, `UPDATE organization_memberships SET security_revision = 3`},
		{"organization revision", `UPDATE organizations SET policy_revision = 3`, `UPDATE organizations SET policy_revision = 2`},
		{"organization suspended", `UPDATE organizations SET status = 'suspended'`, `UPDATE organizations SET status = 'active'`},
		{"session transferred", `UPDATE auth_sessions SET user_id = 8`, `UPDATE auth_sessions SET user_id = 7`},
		{"session revoked", `UPDATE auth_sessions SET revoked_at = now()`, `UPDATE auth_sessions SET revoked_at = NULL`},
		{"account disabled", `UPDATE users SET enabled = false`, `UPDATE users SET enabled = true`},
	} {
		t.Run(test.name, func(t *testing.T) {
			exec(test.change)
			defer exec(test.restore)
			if _, _, err := h.Validate(t.Context(), proof); err == nil {
				t.Fatal("changed authority accepted")
			}
		})
	}
}
