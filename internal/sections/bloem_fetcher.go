package sections

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// PromoSource supplies the active home promotion cards for a profile
// (production: *promotions.Service). LibraryIDs nil means unrestricted access.
type PromoSource interface {
	ActiveHome(ctx context.Context, viewer promotions.Viewer) ([]promotions.Card, int, error)
}

// fetchPromotedSection resolves the S-2 promotion cards for the profile.
// Per-profile (targeting + dismissals), so never cached; the access filter's
// allowed libraries feed library targeting. Cards the caller already
// resolved (ResolvedSection.Promos) are used as is.
func (f *Fetcher) fetchPromotedSection(ctx context.Context, resolved ResolvedSection, userID int, profileID string, filter catalog.AccessFilter) (SectionWithItems, error) {
	result := SectionWithItems{ResolvedSection: resolved, Items: []*models.MediaItem{}}
	cards := resolved.Promos
	if cards == nil {
		if f.Promotions == nil || userID <= 0 {
			return result, nil
		}
		var err error
		cards, _, err = f.Promotions.ActiveHome(ctx, promotions.Viewer{UserID: userID, ProfileID: profileID, LibraryIDs: filter.AllowedLibraryIDs})
		if err != nil {
			return SectionWithItems{}, err
		}
	}
	result.TotalCount = len(cards)
	if resolved.ItemLimit > 0 && len(cards) > resolved.ItemLimit {
		cards = cards[:resolved.ItemLimit]
	}
	result.Promos = cards
	return result, nil
}
