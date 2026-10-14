package entitlements_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/entitlements"
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
	// Revision 1 is the seeded one; later revisions may curate libraries.
	template, err := store.Get(ctx, accesspolicy.PreviewTemplateKey, 1)
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

func TestPreviewTemplateRefusesEveryLoosening(t *testing.T) {
	ctx, _, store := entitlementTestStore(t)
	key := accesspolicy.PreviewTemplateKey
	current, err := store.Latest(ctx, key)
	require.NoError(t, err)
	for name, loosen := range map[string]func(*entitlements.Policy){
		"playback":           func(p *entitlements.Policy) { p.PlaybackAllowed = true },
		"streams":            func(p *entitlements.Policy) { p.MaxStreams = 1 },
		"profiles":           func(p *entitlements.Policy) { p.MaxProfiles = 1 },
		"transcode":          func(p *entitlements.Policy) { p.TranscodeAllowed = true },
		"transcodes":         func(p *entitlements.Policy) { p.MaxTranscodes = 1 },
		"downloads":          func(p *entitlements.Policy) { p.DownloadAllowed = true },
		"download transcode": func(p *entitlements.Policy) { p.DownloadAllowed, p.DownloadTranscodeAllowed = true, true },
		"requests":           func(p *entitlements.Policy) { p.RequestsAllowed = true },
		"quality":            func(p *entitlements.Policy) { p.MaxPlaybackQuality = "1080p" },
		"a permission":       func(p *entitlements.Policy) { p.AllowedPermissions = []string{"marker_edit"} },
		"all permissions":    func(p *entitlements.Policy) { p.AllowedPermissions = nil },
	} {
		policy := current.Policy
		policy.AllowedPermissions = []string{}
		loosen(&policy)
		_, err := store.Revise(ctx, key, current.Revision, entitlements.ReviseTemplateInput{Name: current.Name, Enabled: true, Policy: policy})
		require.ErrorIs(t, err, entitlements.ErrProtectedTemplate, name)
	}
	_, err = store.Revise(ctx, key, current.Revision, entitlements.ReviseTemplateInput{Name: current.Name, Enabled: false, Policy: current.Policy})
	require.ErrorIs(t, err, entitlements.ErrProtectedTemplate, "disable")
	_, err = store.Archive(ctx, key, current.Revision)
	require.ErrorIs(t, err, entitlements.ErrProtectedTemplate, "archive")
	_, err = store.Create(ctx, entitlements.CreateTemplateInput{Key: key, Name: "Shadow", Enabled: true, Policy: current.Policy})
	require.ErrorIs(t, err, entitlements.ErrProtectedTemplate, "re-create")
}

func TestPreviewTemplateAcceptsLibraryCuration(t *testing.T) {
	ctx, pool, store := entitlementTestStore(t)
	key := accesspolicy.PreviewTemplateKey
	current, err := store.Latest(ctx, key)
	require.NoError(t, err)
	libraryID := insertEntitlementLibrary(t, ctx, pool, "Preview curation "+uuid.NewString(), true)
	policy := current.Policy
	policy.LibraryIDs = []int{libraryID}
	revised, err := store.Revise(ctx, key, current.Revision, entitlements.ReviseTemplateInput{Name: current.Name, Enabled: true, Policy: policy})
	require.NoError(t, err)
	require.Equal(t, current.Revision+1, revised.Revision)
	require.ElementsMatch(t, []int{libraryID}, revised.Policy.LibraryIDs)
}

func TestPreviewCohortCannotBeDerived(t *testing.T) {
	fixture := newCohortFixture(t)
	tx, err := fixture.pool.Begin(fixture.ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	parent, _, err := fixture.store.EnsureExactCohortInTx(fixture.ctx, tx, fixture.organizationID, accesspolicy.PreviewTemplateKey, 1, fixture.actorID)
	require.NoError(t, err)
	_, _, err = fixture.store.DeriveCohortInTx(fixture.ctx, tx, fixture.organizationID, parent.ID, "Loosened preview "+uuid.NewString(), entitlements.PolicyPatch{}, fixture.actorID)
	require.ErrorIs(t, err, entitlements.ErrProtectedTemplate)
}
