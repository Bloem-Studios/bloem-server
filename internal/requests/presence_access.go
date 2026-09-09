package requests

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// PresenceScopeResolver supplies the same account/profile scope used by browsing.
type PresenceScopeResolver interface {
	Resolve(context.Context, access.ResolveInput) (access.Scope, error)
}

type PresenceItemAccess interface {
	EnsureAccessibleIDs(context.Context, []string, catalog.AccessFilter) (map[string]bool, error)
}

// SetPresenceAccess bounds availability by the viewer's current catalog access.
// Both HTTP and reconciliation wire their organization-aware resolver here.
func (s *Service) SetPresenceAccess(resolver PresenceScopeResolver, items PresenceItemAccess) {
	s.presenceScope = resolver
	s.presenceItems = items
}

func (s *Service) filterPresence(ctx context.Context, viewer Viewer, matches map[int]PresenceMatch) (map[int]PresenceMatch, error) {
	if s.presenceScope == nil && s.presenceItems == nil {
		return matches, nil
	}
	if s.presenceScope == nil || s.presenceItems == nil {
		return nil, fmt.Errorf("request presence access is not configured")
	}
	scope, err := s.presenceScope.Resolve(ctx, access.ResolveInput{UserID: viewer.UserID, ProfileID: viewer.ProfileID, SkipPINVerification: true})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		if match.Available && match.ContentID != "" {
			ids = append(ids, match.ContentID)
		}
	}
	allowed, err := s.presenceItems.EnsureAccessibleIDs(ctx, ids, catalog.AccessFilter{
		UserID: scope.UserID, ProfileID: scope.ProfileID,
		AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs,
		MaxContentRating: scope.MaxContentRating,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[int]PresenceMatch, len(matches))
	for id, match := range matches {
		if match.Available && allowed[match.ContentID] {
			out[id] = match
		}
	}
	return out, nil
}
