package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

var ErrInvalidAccessGroup = errors.New("invalid access group configuration")
var ErrAccessGroupUnavailable = errors.New("access group administration unavailable")

// memberMovingGroupStore deletes a group after moving its members into the
// default group in the same transaction (access.GroupStore).
type memberMovingGroupStore interface {
	DeleteMovingMembers(context.Context, uuid.UUID, int64, access.GroupPrecondition, func(context.Context, pgx.Tx, []int) error) ([]int, error)
}

type guardedAccessGroupStore interface {
	ListPage(context.Context, uuid.UUID, *access.GroupPageKey, int) ([]access.Group, bool, error)
	UpdateConditional(context.Context, uuid.UUID, int64, access.UpdateGroupInput, access.GroupPrecondition) (*access.Group, error)
	DeleteConditional(context.Context, uuid.UUID, int64, access.GroupPrecondition) error
}

func (h *AccessGroupHandler) GetAdminAccessGroup(ctx context.Context, id int64) (*access.Group, error) {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return nil, err
	}
	if h == nil || h.store == nil {
		return nil, ErrAccessGroupUnavailable
	}
	return h.store.Get(ctx, organizationID, id)
}
func (h *AccessGroupHandler) ListAdminAccessGroupsPage(ctx context.Context, after *access.GroupPageKey, limit int) ([]access.Group, bool, error) {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return nil, false, err
	}
	s, ok := guardedGroupStore(h)
	if !ok {
		return nil, false, ErrAccessGroupUnavailable
	}
	return s.ListPage(ctx, organizationID, after, limit)
}
func (h *AccessGroupHandler) CreateAdminAccessGroup(ctx context.Context, in access.CreateGroupInput) (*access.Group, error) {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return nil, err
	}
	if h == nil || h.store == nil {
		return nil, ErrAccessGroupUnavailable
	}
	update := access.UpdateGroupInput{Name: &in.Name, LibraryIDs: &in.LibraryIDs, MaxPlaybackQuality: &in.MaxPlaybackQuality, MaxStreams: &in.MaxStreams, MaxTranscodes: &in.MaxTranscodes, MaxRemoteStreamBitrateKbps: &in.MaxRemoteStreamBitrateKbps, MaxLocalStreamBitrateKbps: &in.MaxLocalStreamBitrateKbps, AllowedPermissions: &in.AllowedPermissions}
	if err := normalizeAdminGroupInput(&update); err != nil {
		return nil, err
	}
	in.Name = *update.Name
	in.MaxPlaybackQuality = *update.MaxPlaybackQuality
	in.AllowedPermissions = *update.AllowedPermissions
	return h.store.Create(ctx, organizationID, in)
}
func (h *AccessGroupHandler) UpdateAdminAccessGroup(ctx context.Context, id int64, in access.UpdateGroupInput, guard access.GroupPrecondition) (*access.Group, error) {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return nil, err
	}
	s, ok := guardedGroupStore(h)
	if !ok {
		return nil, ErrAccessGroupUnavailable
	}
	if err := normalizeAdminGroupInput(&in); err != nil {
		return nil, err
	}
	return s.UpdateConditional(ctx, organizationID, id, in, guard)
}

// DeleteAdminAccessGroup deletes a group. Its members move into the default
// group in the same transaction, so no regular account is left without a
// group. They stay signed in: the move bumps their access_policy_revision and
// the next request resolves the default group's policy.
func (h *AccessGroupHandler) DeleteAdminAccessGroup(ctx context.Context, id int64, guard access.GroupPrecondition) error {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return err
	}
	s, ok := guardedGroupStore(h)
	if !ok {
		return ErrAccessGroupUnavailable
	}
	mover, ok := h.store.(memberMovingGroupStore)
	if !ok {
		return s.DeleteConditional(ctx, organizationID, id, guard)
	}
	// Set-based, so the group-writer lock is not held for per-member statements.
	_, err = mover.DeleteMovingMembers(ctx, organizationID, id, guard, nil)
	return err
}
func normalizeAdminGroupInput(in *access.UpdateGroupInput) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return ErrInvalidAccessGroup
		}
		in.Name = &name
	}
	if in.LibraryIDs != nil {
		for _, id := range *in.LibraryIDs {
			if id <= 0 {
				return ErrInvalidAccessGroup
			}
		}
	}
	if in.MaxStreams != nil && *in.MaxStreams < 0 || in.MaxTranscodes != nil && *in.MaxTranscodes < 0 || in.MaxRemoteStreamBitrateKbps != nil && *in.MaxRemoteStreamBitrateKbps < 0 || in.MaxLocalStreamBitrateKbps != nil && *in.MaxLocalStreamBitrateKbps < 0 {
		return ErrInvalidAccessGroup
	}
	if in.MaxPlaybackQuality != nil {
		quality, ok := access.ParsePlaybackQualityPreset(*in.MaxPlaybackQuality)
		if !ok {
			return ErrInvalidAccessGroup
		}
		in.MaxPlaybackQuality = &quality
	}
	if in.AllowedPermissions != nil {
		permissions, err := auth.NormalizePermissions(*in.AllowedPermissions)
		if err != nil {
			return ErrInvalidAccessGroup
		}
		in.AllowedPermissions = &permissions
	}
	return nil
}

func guardedGroupStore(h *AccessGroupHandler) (guardedAccessGroupStore, bool) {
	if h == nil {
		return nil, false
	}
	s, ok := h.store.(guardedAccessGroupStore)
	return s, ok
}
