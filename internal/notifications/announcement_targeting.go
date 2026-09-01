package notifications

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

// recipientSource is what targeting resolution reads. Production is the
// System (accounts, per-user profile stores, tenancy memberships, access
// scopes); tests substitute an in-memory fake.
type recipientSource interface {
	ListUsers(ctx context.Context) ([]*models.User, error)
	ListProfiles(ctx context.Context, userID int) ([]userstore.Profile, error)
	OrganizationAccountIDs(ctx context.Context, organizationID uuid.UUID) ([]int, error)
	ProfileLibraryAllowed(ctx context.Context, userID int, profileID string, libraryID int) (bool, error)
}

// validateTargeting canonicalizes the audience and checks its side fields.
func validateTargeting(in AnnouncementTargeting) (AnnouncementTargeting, error) {
	out := in
	out.Audience = strings.ToLower(strings.TrimSpace(out.Audience))
	out.Role = strings.ToLower(strings.TrimSpace(out.Role))
	out.OrganizationID = strings.TrimSpace(out.OrganizationID)
	switch out.Audience {
	case "":
		out.Audience = AudienceAll
	case AudienceAll:
	case AudienceRole:
		if out.Role != models.RoleAdmin && out.Role != models.RoleUser {
			return out, fmt.Errorf("%w: role must be admin or user", ErrAnnouncementInvalid)
		}
	case AudienceOrganization:
		if _, err := uuid.Parse(out.OrganizationID); err != nil {
			return out, fmt.Errorf("%w: organization_id must be a UUID", ErrAnnouncementInvalid)
		}
	case AudienceLibrary:
		if out.LibraryID <= 0 {
			return out, fmt.Errorf("%w: library_id is required", ErrAnnouncementInvalid)
		}
	case AudienceExplicit:
		if len(out.UserIDs) == 0 && len(out.ProfileIDs) == 0 {
			return out, fmt.Errorf("%w: explicit targeting needs user_ids or profile_ids", ErrAnnouncementInvalid)
		}
	default:
		return out, fmt.Errorf("%w: unknown audience %q", ErrAnnouncementInvalid, out.Audience)
	}
	// Side fields outside their audience are dropped, not stored.
	if out.Audience != AudienceRole {
		out.Role = ""
	}
	if out.Audience != AudienceOrganization {
		out.OrganizationID = ""
	}
	if out.Audience != AudienceLibrary {
		out.LibraryID = 0
	}
	if out.Audience != AudienceExplicit {
		out.UserIDs, out.ProfileIDs = nil, nil
	}
	return out, nil
}

// resolveAnnouncementRecipients expands validated targeting into the
// distinct (user, profile) set. Disabled accounts never receive anything.
func resolveAnnouncementRecipients(ctx context.Context, src recipientSource, targeting AnnouncementTargeting) ([]AnnouncementRecipient, error) {
	users, err := src.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	byID := make(map[int]*models.User, len(users))
	for _, user := range users {
		if user != nil && user.Enabled {
			byID[user.ID] = user
		}
	}

	candidates := make([]int, 0, len(byID))
	switch targeting.Audience {
	case AudienceAll, AudienceLibrary:
		for id := range byID {
			candidates = append(candidates, id)
		}
	case AudienceRole:
		for id, user := range byID {
			if user.Role == targeting.Role {
				candidates = append(candidates, id)
			}
		}
	case AudienceOrganization:
		orgID, err := uuid.Parse(targeting.OrganizationID)
		if err != nil {
			return nil, fmt.Errorf("%w: organization_id must be a UUID", ErrAnnouncementInvalid)
		}
		accountIDs, err := src.OrganizationAccountIDs(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("list organization members: %w", err)
		}
		for _, id := range accountIDs {
			if _, ok := byID[id]; ok {
				candidates = append(candidates, id)
			}
		}
	case AudienceExplicit:
		for _, id := range targeting.UserIDs {
			if _, ok := byID[id]; ok {
				candidates = append(candidates, id)
			}
		}
	}
	sort.Ints(candidates)

	seen := make(map[string]struct{})
	out := make([]AnnouncementRecipient, 0, len(candidates))
	add := func(userID int, profileID string) {
		if _, ok := seen[profileID]; ok {
			return
		}
		seen[profileID] = struct{}{}
		out = append(out, AnnouncementRecipient{UserID: userID, ProfileID: profileID})
	}
	for _, userID := range candidates {
		profiles, err := src.ListProfiles(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list profiles for user %d: %w", userID, err)
		}
		for _, profile := range profiles {
			if targeting.Audience == AudienceLibrary {
				allowed, err := src.ProfileLibraryAllowed(ctx, userID, profile.ID, targeting.LibraryID)
				if err != nil {
					return nil, fmt.Errorf("resolve library access for profile %s: %w", profile.ID, err)
				}
				if !allowed {
					continue
				}
			}
			add(userID, profile.ID)
		}
	}
	if targeting.Audience == AudienceExplicit && len(targeting.ProfileIDs) > 0 {
		// Explicit profiles need their owning account; scan enabled accounts
		// (bounded by the deployment's user count, an admin-only path).
		wanted := make(map[string]struct{}, len(targeting.ProfileIDs))
		for _, id := range targeting.ProfileIDs {
			wanted[id] = struct{}{}
		}
		ids := make([]int, 0, len(byID))
		for id := range byID {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, userID := range ids {
			if len(wanted) == 0 {
				break
			}
			profiles, err := src.ListProfiles(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("list profiles for user %d: %w", userID, err)
			}
			for _, profile := range profiles {
				if _, ok := wanted[profile.ID]; ok {
					add(userID, profile.ID)
					delete(wanted, profile.ID)
				}
			}
		}
	}
	return out, nil
}

// systemRecipientSource adapts the System's collaborators to recipientSource.
type systemRecipientSource struct {
	system *System
}

func (s systemRecipientSource) ListUsers(ctx context.Context) ([]*models.User, error) {
	if s.system.users == nil {
		return nil, nil
	}
	return s.system.users.List(ctx)
}

func (s systemRecipientSource) ListProfiles(ctx context.Context, userID int) ([]userstore.Profile, error) {
	if s.system.stores == nil {
		return nil, nil
	}
	store, err := s.system.stores.ForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return store.ListProfiles(ctx)
}

func (s systemRecipientSource) OrganizationAccountIDs(ctx context.Context, organizationID uuid.UUID) ([]int, error) {
	rows, err := s.system.pool.Query(ctx, `
		SELECT account_id FROM organization_memberships
		WHERE organization_id = $1 AND status = 'active'
		ORDER BY account_id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int, 0, 16)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s systemRecipientSource) ProfileLibraryAllowed(ctx context.Context, userID int, profileID string, libraryID int) (bool, error) {
	if s.system.scopes == nil {
		return false, nil
	}
	scope, err := s.system.scopes.Resolve(ctx, access.ResolveInput{
		UserID:              userID,
		ProfileID:           profileID,
		SkipPINVerification: true,
	})
	if err != nil {
		return false, err
	}
	if !scope.LibrariesRestricted {
		return true, nil
	}
	for _, id := range scope.AllowedLibraryIDs {
		if id == libraryID {
			return true, nil
		}
	}
	return false, nil
}
