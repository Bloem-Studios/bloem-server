package sections

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// SectionPromoted is the existing Bloem promotion-card section type.
const SectionPromoted SectionType = "promoted"

func init() { ValidSectionTypes[SectionPromoted] = true }

// PromoSource supplies active home promotion cards for a profile.
type PromoSource interface {
	ActiveHome(context.Context, promotions.Viewer) ([]promotions.Card, int, error)
}

// PromotionCards reads cards carried between the owned layout/fetch/projection adapters.
func PromotionCards(section ResolvedSection) []promotions.Card {
	cards, _ := section.ExtensionData.([]promotions.Card)
	return cards
}

// InstallPromotions registers the promotion resolver, including an explicitly
// unavailable source. An unavailable source produces an empty optional section.
func InstallPromotions(fetcher *Fetcher, source PromoSource) {
	if fetcher.SectionResolvers == nil {
		fetcher.SectionResolvers = map[SectionType]SectionResolver{}
	}
	fetcher.SectionResolvers[SectionPromoted] = func(ctx context.Context, request SectionFetchRequest) (SectionWithItems, error) {
		resolved := request.Section
		result := SectionWithItems{ResolvedSection: resolved, Items: []*models.MediaItem{}}
		cards := PromotionCards(resolved)
		if cards == nil {
			if source == nil || request.UserID <= 0 {
				return result, nil
			}
			var err error
			cards, _, err = source.ActiveHome(ctx, promotions.Viewer{UserID: request.UserID, ProfileID: request.ProfileID, LibraryIDs: request.Access.AllowedLibraryIDs})
			if err != nil {
				return SectionWithItems{}, err
			}
		}
		result.TotalCount = len(cards)
		if resolved.ItemLimit > 0 && len(cards) > resolved.ItemLimit {
			cards = cards[:resolved.ItemLimit]
		}
		result.ExtensionData = cards
		return result, nil
	}
}
