package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func (s *TenantGroupStore) Create(ctx context.Context, org uuid.UUID, in CreateGroupInput) (*TenantGroup, error) {
	return s.CreatePolicy(ctx, org, TenantCreateGroupInput{CreateGroupInput: in})
}
func (s *TenantGroupStore) Update(ctx context.Context, org uuid.UUID, id int64, in UpdateGroupInput) (*TenantGroup, error) {
	return s.UpdateConditional(ctx, org, id, in, GroupPrecondition{Any: true})
}
func (s *TenantGroupStore) UpdateConditional(ctx context.Context, org uuid.UUID, id int64, in UpdateGroupInput, guard GroupPrecondition) (*TenantGroup, error) {
	return s.UpdatePolicyConditional(ctx, org, id, TenantUpdateGroupInput{UpdateGroupInput: in}, guard)
}

type tenantGroupPatch struct {
	sets []string
	args []any
}

func (p *tenantGroupPatch) assign(column string, value any) {
	p.args = append(p.args, value)
	p.sets = append(p.sets, fmt.Sprintf("%s = $%d", column, len(p.args)))
}

// Create inserts a new access group.
func (s *TenantGroupStore) CreatePolicy(ctx context.Context, organizationID uuid.UUID, input TenantCreateGroupInput) (*TenantGroup, error) {
	if organizationID == uuid.Nil {
		return nil, ErrGroupNotFound
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("access group name is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning access group create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTenantGroupWriters(ctx, tx); err != nil {
		return nil, err
	}
	if input.IsDefault {
		if err := protectManagedDefault(ctx, tx, organizationID, 0); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET is_default = false
			WHERE organization_id = $1
			  AND is_default`, organizationID); err != nil {
			return nil, fmt.Errorf("clearing previous default access group: %w", err)
		}
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO access_groups (
			organization_id, name, description, library_ids, max_playback_quality,
			playback_allowed, download_allowed, download_transcode_allowed,
			transcode_allowed, audio_transcode_allowed, max_streams, max_profiles,
			max_transcodes, max_remote_stream_bitrate_kbps, max_local_stream_bitrate_kbps, allowed_permissions, requests_allowed, is_default
		)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, true), $7, $8, $9, $10,
		        $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id`,
		organizationID,
		name,
		input.Description,
		input.LibraryIDs,
		NormalizePlaybackQuality(input.MaxPlaybackQuality),
		input.PlaybackAllowed,
		input.DownloadAllowed,
		input.DownloadTranscodeAllowed,
		input.TranscodeAllowed,
		input.AudioTranscodeAllowed,
		input.MaxStreams,
		input.MaxProfiles,
		input.MaxTranscodes,
		input.MaxRemoteStreamBitrateKbps,
		input.MaxLocalStreamBitrateKbps,
		input.AllowedPermissions,
		input.RequestsAllowed,
		input.IsDefault,
	).Scan(&id)
	if err != nil {
		if isGroupDuplicate(err) {
			return nil, ErrGroupDuplicate
		}
		return nil, fmt.Errorf("creating access group: %w", err)
	}
	result, err := getTenantGroup(ctx, tx, organizationID, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing access group create: %w", err)
	}
	return result, nil
}

func (s *TenantGroupStore) UpdatePolicyConditional(ctx context.Context, organizationID uuid.UUID, id int64, input TenantUpdateGroupInput, guard GroupPrecondition) (*TenantGroup, error) {
	if !guard.valid() {
		return nil, ErrGroupInvalidPrecondition
	}

	patch := tenantGroupPatch{}
	if input.Name != nil {
		patch.assign("name", strings.TrimSpace(*input.Name))
	}
	if input.Description != nil {
		patch.assign("description", *input.Description)
	}
	if input.LibraryIDs != nil {
		patch.assign("library_ids", *input.LibraryIDs)
	}
	if input.MaxPlaybackQuality != nil {
		patch.assign("max_playback_quality", NormalizePlaybackQuality(*input.MaxPlaybackQuality))
	}
	if input.PlaybackAllowed != nil {
		patch.assign("playback_allowed", *input.PlaybackAllowed)
	}
	if input.DownloadAllowed != nil {
		patch.assign("download_allowed", *input.DownloadAllowed)
	}
	if input.DownloadTranscodeAllowed != nil {
		patch.assign("download_transcode_allowed", *input.DownloadTranscodeAllowed)
	}
	if input.TranscodeAllowed != nil {
		patch.assign("transcode_allowed", *input.TranscodeAllowed)
	}
	if input.AudioTranscodeAllowed != nil {
		patch.assign("audio_transcode_allowed", *input.AudioTranscodeAllowed)
	}
	if input.MaxStreams != nil {
		patch.assign("max_streams", *input.MaxStreams)
	}
	if input.MaxProfiles != nil {
		patch.assign("max_profiles", *input.MaxProfiles)
	}
	if input.MaxTranscodes != nil {
		patch.assign("max_transcodes", *input.MaxTranscodes)
	}
	if input.MaxRemoteStreamBitrateKbps != nil {
		patch.assign("max_remote_stream_bitrate_kbps", *input.MaxRemoteStreamBitrateKbps)
	}
	if input.MaxLocalStreamBitrateKbps != nil {
		patch.assign("max_local_stream_bitrate_kbps", *input.MaxLocalStreamBitrateKbps)
	}
	if input.AllowedPermissions != nil {
		patch.assign("allowed_permissions", *input.AllowedPermissions)
	}
	if input.RequestsAllowed != nil {
		patch.assign("requests_allowed", *input.RequestsAllowed)
	}
	if input.IsDefault != nil {
		patch.assign("is_default", *input.IsDefault)
	}
	sets, args := patch.sets, patch.args
	arg := len(args) + 1

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning access group update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTenantGroupWriters(ctx, tx); err != nil {
		return nil, err
	}
	currentGroup, err := lockTenantGroup(ctx, tx, organizationID, id, guard)
	if err != nil {
		return nil, err
	}
	current := *currentGroup
	if len(sets) == 0 {
		return currentGroup, nil
	}
	if current.ManagedTemplateKey != nil || current.ManagedCohortID != uuid.Nil {
		return nil, ErrManagedGroup
	}
	authorizationChanged := groupAuthorizationChanged(current, input)
	if input.IsDefault != nil && *input.IsDefault {
		if err := protectManagedDefault(ctx, tx, organizationID, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET is_default = false
			WHERE organization_id = $1
			  AND is_default
			  AND id <> $2`, organizationID, id); err != nil {
			return nil, fmt.Errorf("clearing previous default access group: %w", err)
		}
	}
	if input.IsDefault != nil && !*input.IsDefault {
		if current.IsDefault {
			return nil, ErrDefaultGroupRequired
		}
	}

	sets = append(sets, "updated_at = NOW()")
	query := fmt.Sprintf("UPDATE access_groups SET %s WHERE organization_id = $%d AND id = $%d", strings.Join(sets, ", "), arg, arg+1)
	args = append(args, organizationID, id)
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		if isGroupDuplicate(err) {
			return nil, ErrGroupDuplicate
		}
		return nil, fmt.Errorf("updating access group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrGroupNotFound
	}
	if authorizationChanged {
		if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE organization_memberships
			SET access_policy_revision = access_policy_revision + 1
			WHERE organization_id = $1
			  AND account_id IN (
				SELECT DISTINCT user_id
				FROM user_profiles
				WHERE organization_id = $1
				  AND access_group_id = $2
			)`, organizationID, id); err != nil {
			return nil, fmt.Errorf("bumping access group member revisions: %w", err)
		}
	}
	result, err := getTenantGroup(ctx, tx, organizationID, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing access group update: %w", err)
	}
	return result, nil
}

// Delete removes an access group. Canonical profile assignments are moved to
// the organization's default group; the legacy account assignment is cleared
// by its FK. The default group cannot be deleted — promote another group first.
func (s *TenantGroupStore) Delete(ctx context.Context, organizationID uuid.UUID, id int64) error {
	_, err := s.DeleteWithImpact(ctx, organizationID, id)
	return err
}

// DeleteWithImpact removes an access group and returns the exact number of
// profiles moved to the organization's default group.
func (s *TenantGroupStore) DeleteConditional(ctx context.Context, organizationID uuid.UUID, id int64, guard GroupPrecondition) error {
	_, err := s.deleteConditionalWithImpact(ctx, organizationID, id, guard)
	return err
}
