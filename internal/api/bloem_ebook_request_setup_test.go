package api

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
)

func TestBloemEbookRequestSetupPolicyDurableDefaultAndAuthorityDatabase(t *testing.T) {
	pool := newDisposableAPIDatabase(t, "bloem_ebook_setup_test_", true)
	if err := database.RunMigrations(t.Context(), pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	var v bookrequests.Viewer
	v.Admin = true
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&v.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('ebook-admin','ebook-admin@example.test','x','admin') RETURNING id`).Scan(&v.UserID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err := tenancy.MarkMembershipPolicyWriter(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','admin') ON CONFLICT DO NOTHING`, v.OrganizationID, v.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := bloemEbookSetup{pool: pool, books: bookrequests.NewService(pool, nil), platform: auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(pool))}
	setup, err := s.List(t.Context(), v)
	if err != nil || !setup.Available || setup.Enabled {
		t.Fatalf("default-off setup: %+v %v", setup, err)
	}
	for _, enabled := range []bool{true, false} {
		if err := s.SetEnabled(t.Context(), v, enabled); err != nil {
			t.Fatal(err)
		}
		setup, err = s.List(t.Context(), v)
		if err != nil || setup.Enabled != enabled {
			t.Fatalf("policy readback: %+v %v", setup, err)
		}
		var value string
		if err := pool.QueryRow(t.Context(), `SELECT value FROM server_settings WHERE key=$1`, config.EbookRequestsEnabledSettingKey).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if enabled && value != "true" || !enabled && value != "false" {
			t.Fatalf("policy not canonical boolean: %q", value)
		}
	}
	foreign := v
	foreign.OrganizationID = uuid.New()
	if err := s.SetEnabled(t.Context(), foreign, true); !errors.Is(err, bookrequests.ErrForbidden) {
		t.Fatalf("foreign config allowed: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET role='user' WHERE id=$1`, v.UserID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(t.Context(), v, true); !errors.Is(err, bookrequests.ErrForbidden) {
		t.Fatalf("revoked platform authority allowed: %v", err)
	}
}
