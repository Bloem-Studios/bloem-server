package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SiloSwitchingReport describes what EnableSiloSwitching changed.
type SiloSwitchingReport struct {
	AlreadyMirrored    bool
	Accounts           int
	MembershipsCreated int
	PoliciesReconciled int
}

// EnableSiloSwitching moves a compatibility-phase database to 'mirrored', the
// phase in which upstream Silo and Bloem can each serve it: triggers keep the
// legacy users policy columns and the default-organization membership
// identical. It refuses what Silo could not honor: more than one organization,
// or a profile with its own login. users is authoritative at the moment of the
// switch, because compatibility froze policy on both sides.
func EnableSiloSwitching(ctx context.Context, pool *pgxpool.Pool) (SiloSwitchingReport, error) {
	var report SiloSwitchingReport
	if pool == nil {
		return report, errors.New("tenancy: enable silo switching requires a database pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("tenancy: begin enable silo switching: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The same exclusive handoff lock finalize takes: no heartbeat or policy
	// writer can interleave with the phase change.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bloem.membership_policy_handoff', 0))`); err != nil {
		return report, fmt.Errorf("tenancy: lock membership policy handoff: %w", err)
	}
	var phase string
	if err := tx.QueryRow(ctx, `SELECT phase FROM public.membership_policy_authority WHERE singleton FOR UPDATE`).Scan(&phase); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return report, errors.New("tenancy: membership policy authority row is missing")
		}
		return report, fmt.Errorf("tenancy: read membership policy authority: %w", err)
	}
	switch phase {
	case "mirrored":
		report.AlreadyMirrored = true
		return report, tx.Commit(ctx)
	case "finalized":
		return report, errors.New("tenancy: membership policy is finalized; Silo switching is no longer possible")
	}

	var organizations, directLogins int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organizations),
		       (SELECT count(*) FROM user_profiles WHERE login_email IS NOT NULL OR password_hash IS NOT NULL)`,
	).Scan(&organizations, &directLogins); err != nil {
		return report, fmt.Errorf("tenancy: silo switching preflight: %w", err)
	}
	if organizations != 1 {
		return report, fmt.Errorf("tenancy: silo switching needs exactly one organization, found %d", organizations)
	}
	if directLogins != 0 {
		return report, fmt.Errorf("tenancy: silo switching cannot keep %d profiles with a direct profile login", directLogins)
	}

	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_mirror_enabler', 'v1', true)`); err != nil {
		return report, fmt.Errorf("tenancy: mark silo switching enabler: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE public.membership_policy_authority SET phase = 'mirrored', mirrored_at = now() WHERE singleton`); err != nil {
		return report, fmt.Errorf("tenancy: enter mirrored phase: %w", err)
	}
	// Accounts Silo created after Bloem's migrations have no membership yet.
	// The unmarked insert is seeded from users by seed_legacy_membership_policy.
	tag, err := tx.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT public.bloem_default_organization_id(), users.id, 'active',
		       CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END
		FROM users
		WHERE NOT EXISTS (SELECT 1 FROM organization_memberships m WHERE m.account_id = users.id)`)
	if err != nil {
		return report, fmt.Errorf("tenancy: create missing default memberships: %w", err)
	}
	report.MembershipsCreated = int(tag.RowsAffected())

	// Reconcile from users. The writer marker admits the write past the
	// membership guard; the mirroring setting keeps the reverse mirror quiet.
	if _, err := tx.Exec(ctx, `
		SELECT set_config('bloem.membership_policy_writer', 'v1', true),
		       set_config('bloem.membership_policy_mirroring', 'on', true)`); err != nil {
		return report, fmt.Errorf("tenancy: mark reconcile writer: %w", err)
	}
	tag, err = tx.Exec(ctx, `
		UPDATE organization_memberships AS m
		SET access_group_id = u.access_group_id, permissions = u.permissions, library_ids = u.library_ids,
		    max_playback_quality = u.max_playback_quality, max_streams = u.max_streams,
		    max_transcodes = u.max_transcodes, transcode_allowed = u.transcode_allowed,
		    audio_transcode_allowed = u.audio_transcode_allowed, download_allowed = u.download_allowed,
		    download_transcode_allowed = u.download_transcode_allowed, requests_allowed = u.requests_allowed,
		    max_profiles = u.max_profiles, access_policy_revision = u.access_policy_revision, updated_at = now()
		FROM users AS u
		WHERE m.account_id = u.id
		  AND m.organization_id = public.bloem_default_organization_id()
		  AND ROW(m.access_group_id, m.permissions, m.library_ids, m.max_playback_quality, m.max_streams,
		          m.max_transcodes, m.transcode_allowed, m.audio_transcode_allowed, m.download_allowed,
		          m.download_transcode_allowed, m.requests_allowed, m.max_profiles, m.access_policy_revision)
		      IS DISTINCT FROM
		      ROW(u.access_group_id, u.permissions, u.library_ids, u.max_playback_quality, u.max_streams,
		          u.max_transcodes, u.transcode_allowed, u.audio_transcode_allowed, u.download_allowed,
		          u.download_transcode_allowed, u.requests_allowed, u.max_profiles, u.access_policy_revision)`)
	if err != nil {
		return report, fmt.Errorf("tenancy: reconcile membership policy from users: %w", err)
	}
	report.PoliciesReconciled = int(tag.RowsAffected())
	// Roles are not frozen in compatibility, so Silo may have changed one.
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships AS m
		SET legacy_role = CASE WHEN u.role = 'admin' THEN 'admin' ELSE 'user' END,
		    security_revision = m.security_revision + 1, updated_at = now()
		FROM users AS u
		WHERE m.account_id = u.id
		  AND m.organization_id = public.bloem_default_organization_id()
		  AND (m.legacy_role = 'admin') IS DISTINCT FROM (u.role = 'admin')`); err != nil {
		return report, fmt.Errorf("tenancy: reconcile membership roles from users: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT set_config('bloem.membership_policy_writer', '', true),
		       set_config('bloem.membership_policy_mirroring', '', true)`); err != nil {
		return report, fmt.Errorf("tenancy: clear reconcile writer: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&report.Accounts); err != nil {
		return report, fmt.Errorf("tenancy: count accounts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("tenancy: commit enable silo switching: %w", err)
	}
	return report, nil
}
