package handlers

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

var ErrInvalidAccessGroup = errors.New("invalid access group configuration")
var ErrAccessGroupUnavailable = errors.New("access group administration unavailable")

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
	update := access.UpdateGroupInput{Name: &in.Name, LibraryIDs: &in.LibraryIDs, MaxPlaybackQuality: &in.MaxPlaybackQuality, MaxStreams: &in.MaxStreams, MaxTranscodes: &in.MaxTranscodes, AllowedPermissions: &in.AllowedPermissions}
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
func (h *AccessGroupHandler) DeleteAdminAccessGroup(ctx context.Context, id int64, guard access.GroupPrecondition) error {
	organizationID, err := adminGroupOrganization(ctx)
	if err != nil {
		return err
	}

	s, ok := guardedGroupStore(h)
	if !ok {
		return ErrAccessGroupUnavailable
	}
	return s.DeleteConditional(ctx, organizationID, id, guard)
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
	if in.MaxStreams != nil && *in.MaxStreams < 0 || in.MaxTranscodes != nil && *in.MaxTranscodes < 0 {
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

func adminGroupOrganization(ctx context.Context) (uuid.UUID, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.OrganizationID == uuid.Nil {
		return uuid.Nil, apiError(503, "tenant_unavailable", "Tenant authorization is unavailable")
	}
	return tenant.OrganizationID, nil
}
