package auth

import (
	"context"
	"testing"
	"time"

	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/models"
)

// previewFixture creates an account in the default organization and returns
// its id, a live session id, and the id of a preview group in that
// organization (materialized directly, matching the template's invariants).
func previewFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string) (accountID int, sessionID string, previewGroupID int64) {
	t.Helper()
	users := NewUserRepository(pool)
	name := "preview-" + uuid.NewString()[:8]
	user, err := users.Create(ctx, models.CreateUserInput{Username: name, Email: name + "@example.test", Password: "synthetic-password", Role: role})
	if err != nil {
		t.Fatal(err)
	}
	sessionID = "preview-session-" + uuid.NewString()
	if err := NewSessionRepository(pool).Create(ctx, models.AuthSession{ID: sessionID, UserID: user.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (organization_id, name, description, is_default, managed_template_key,
			managed_template_revision, playback_allowed, max_streams, max_profiles, transcode_allowed,
			max_transcodes, download_allowed, download_transcode_allowed, max_playback_quality,
			allowed_permissions, requests_allowed)
		SELECT id, $1, 'preview principal test', false, 'bloem-preview', 1, false, 0, 0, false, 0,
			false, false, '', '{}', false
		FROM organizations WHERE is_default RETURNING id`, "Preview "+uuid.NewString()).Scan(&previewGroupID); err != nil {
		t.Fatal(err)
	}
	return user.ID, sessionID, previewGroupID
}

func assignMembershipGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID int, groupID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		UPDATE organization_memberships SET access_group_id=$2, access_policy_revision=access_policy_revision+1
		WHERE account_id=$1 AND set_config('bloem.membership_policy_writer', public.bloem_membership_policy_writer_marker(), true) IS NOT NULL`,
		accountID, groupID); err != nil {
		t.Fatal(err)
	}
}

func TestActiveSessionAuthorityReadsTheGroupLive(t *testing.T) {
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	repo := NewSessionRepository(pool)
	accountID, sessionID, previewGroupID := previewFixture(t, ctx, pool, "user")

	role, preview, active, err := repo.ActiveSessionAuthority(ctx, sessionID)
	if err != nil || !active || role != "user" || preview {
		t.Fatalf("before: role=%q preview=%v active=%v err=%v", role, preview, active, err)
	}
	assignMembershipGroup(t, ctx, pool, accountID, previewGroupID)
	if _, preview, _, _ = repo.ActiveSessionAuthority(ctx, sessionID); !preview {
		t.Fatal("the same session did not become a preview principal after its group changed")
	}
	if accountPreview, err := repo.AccountPreviewOnly(ctx, accountID); err != nil || !accountPreview {
		t.Fatalf("AccountPreviewOnly = %v, %v", accountPreview, err)
	}
	if _, _, active, err = repo.ActiveSessionAuthority(ctx, "missing-session"); active || err != nil {
		t.Fatalf("missing session: active=%v err=%v", active, err)
	}
}

func TestActiveSessionAuthorityCountsProfileGroups(t *testing.T) {
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	accountID, sessionID, previewGroupID := previewFixture(t, ctx, pool, "user")
	// users.Create makes no profile; add one in the preview group while the
	// account's own membership keeps its default group.
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (id, user_id, name, organization_id, access_group_id)
		SELECT $1, $2, 'Preview profile', organization_id, id FROM access_groups WHERE id = $3`,
		uuid.NewString(), accountID, previewGroupID); err != nil {
		t.Fatal(err)
	}
	if _, preview, _, err := NewSessionRepository(pool).ActiveSessionAuthority(ctx, sessionID); err != nil || !preview {
		t.Fatalf("profile in a preview group: preview=%v err=%v", preview, err)
	}
}

func TestAdminIsNeverAPreviewPrincipal(t *testing.T) {
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	accountID, sessionID, previewGroupID := previewFixture(t, ctx, pool, "admin")
	assignMembershipGroup(t, ctx, pool, accountID, previewGroupID)
	if _, preview, _, err := NewSessionRepository(pool).ActiveSessionAuthority(ctx, sessionID); err != nil || preview {
		t.Fatalf("admin: preview=%v err=%v", preview, err)
	}
}

func TestPreviewAccountSQLNamesTheTemplateKey(t *testing.T) {
	if !strings.Contains(previewAccountSQL, "'"+accesspolicy.PreviewTemplateKey+"'") {
		t.Fatal("previewAccountSQL does not name accesspolicy.PreviewTemplateKey")
	}
}

func TestActiveSessionAuthorityEndsAnIdleGuest(t *testing.T) {
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	user, err := NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "idle-guest", Email: "idle-guest@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_settings SET account_id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	guests := NewPreviewGuests(pool, NewJWTService("s", time.Hour, time.Hour), nil)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := guests.CreateInTx(ctx, tx, NewPreviewGuest{AccountID: user.ID, ExpiresAt: time.Now().Add(time.Hour), IdleTTL: 20 * time.Minute, ClientBucket: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewSessionRepository(pool)
	if _, _, active, err := repo.ActiveSessionAuthority(ctx, pair.SessionID); err != nil || !active {
		t.Fatalf("fresh guest active=%v err=%v", active, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET last_seen_at = now() - interval '21 minutes', created_at = now() - interval '40 minutes' WHERE id=$1`, pair.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, active, err := repo.ActiveSessionAuthority(ctx, pair.SessionID); err != nil || active {
		t.Fatalf("idle guest active=%v err=%v", active, err)
	}
}
