package entitlements_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
)

func requireCheckViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want check violation %s", err, constraint)
	}
}

func TestPreviewTemplateIsSeededWithItsInvariants(t *testing.T) {
	ctx, _, store := entitlementTestStore(t)
	template, err := store.Latest(ctx, accesspolicy.PreviewTemplateKey)
	require.NoError(t, err)
	require.True(t, template.Enabled)
	require.False(t, template.Archived)
	p := template.Policy
	require.False(t, p.PlaybackAllowed || p.TranscodeAllowed || p.DownloadAllowed || p.DownloadTranscodeAllowed || p.RequestsAllowed)
	require.Equal(t, 0, p.MaxStreams+p.MaxProfiles+p.MaxTranscodes)
	require.Equal(t, "", p.MaxPlaybackQuality)
	require.True(t, p.AllowedPermissions != nil && len(p.AllowedPermissions) == 0, "permissions must be an explicit empty set")
	require.Nil(t, p.LibraryIDs)
}

func TestPreviewInvariantChecksRejectLooseningInTheDatabase(t *testing.T) {
	ctx, pool, _ := entitlementTestStore(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO entitlement_template_revisions (
			template_key, revision, library_ids, playback_allowed, max_streams, max_profiles,
			transcode_allowed, max_transcodes, download_allowed, download_transcode_allowed,
			max_playback_quality, allowed_permissions, requests_allowed)
		VALUES ('bloem-preview', 999, NULL, true, 0, 0, false, 0, false, false, '', '{}', false)`)
	requireCheckViolation(t, err, "entitlement_template_revisions_preview_invariant")

	_, err = pool.Exec(ctx, `
		INSERT INTO access_groups (organization_id, name, description, is_default,
			managed_template_key, managed_template_revision, playback_allowed)
		SELECT id, $1, 'preview invariant probe', false, 'bloem-preview', 1, true
		FROM organizations WHERE is_default`, "Preview probe "+uuid.NewString())
	requireCheckViolation(t, err, "access_groups_preview_invariant")
}
