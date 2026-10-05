package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/sections"
)

// validatePromotedScope keeps promoted rows on the home surface only: that is
// the sole surface whose handlers apply the promoted=1 opt-in gate, so a
// promoted row anywhere else would reach clients that never asked for it.
func validatePromotedScope(sectionType sections.SectionType, scope string) (string, bool) {
	if sectionType == sections.SectionPromoted && scope != "" && scope != "home" {
		return "promoted sections are only valid in the home scope", false
	}
	return "", true
}

// sectionSettingsEntry is one row of the profile section-settings listing:
// the resolved section plus the profile-override flags the settings screen
// renders. Named (not an inline literal) so the client DTO registry can
// generate it; field names, tags and population order are the wire contract.
type sectionSettingsEntry struct {
	ID          string          `json:"id"`
	SectionType string          `json:"section_type"`
	Title       string          `json:"title"`
	Featured    bool            `json:"featured"`
	ItemLimit   int             `json:"item_limit"`
	Hidden      bool            `json:"hidden"`
	IsCustom    bool            `json:"is_custom"`
	Customized  bool            `json:"customized"`
	Position    int             `json:"position"`
	Config      json.RawMessage `json:"config,omitempty"`
}

// sectionSettingsResponse wraps the settings listing. It replaces an inline
// map[string][]settingsEntry literal; the marshaled bytes are identical.
type sectionSettingsResponse struct {
	Sections []sectionSettingsEntry `json:"sections"`
}

// promoSectionItem projects an S-2 card onto the section item union: the
// `promo` variant (type "promo", content_id = promotion id, title =
// headline). Media fields stay at their zero values.
func promoSectionItem(card promotions.Card) sectionItemResponse {
	c := card
	return sectionItemResponse{
		ContentID: c.ID,
		Type:      "promo",
		Title:     c.Headline,
		Genres:    []string{},
		Keywords:  []string{},
		Promo:     &c,
	}
}

// SystemPromotedSectionID is the id of the synthetic S-2 home section.
const SystemPromotedSectionID = "system-promoted"

// promotedOptInParam is the query parameter a client sends on the home
// endpoints to receive the S-2 `promoted` section. Pre-S-2 and
// upstream-compat clients decode section_type as a plain string with no
// unknown-type drop, so the row is never delivered unless asked for.
const promotedOptInParam = "promoted"

// wantsPromoted reports whether the request opted in to the `promoted`
// section (`promoted=1`). Absent or any other value keeps the response
// identical to a build without S-2.
func wantsPromoted(r *http.Request) bool {
	return r.URL.Query().Get(promotedOptInParam) == "1"
}

// maybeInjectPromoted delivers the S-2 `promoted` home section to clients
// that opted in with `promoted=1`. Without the opt-in every promoted row —
// synthetic or admin-pinned — is dropped from the layout. With it, when the
// profile has active home cards and the admin layout carries no promoted
// section of its own, a synthetic row lands at the first card's
// placement.home_position (default promotions.DefaultHomePosition), clamped
// to the layout; the resolved cards ride on the row so the fetcher does not
// query them again.
func (h *SectionHandler) maybeInjectPromoted(r *http.Request, resolved []sections.ResolvedSection) []sections.ResolvedSection {
	return h.transformHomeSections(r.Context(), resolved, r.URL.Query())
}

// WithHomePromotions retains the existing opt-in helper for native callers.
func WithHomePromotions(ctx context.Context, enabled bool) context.Context {
	options := url.Values{}
	if enabled {
		options.Set(promotedOptInParam, "1")
	}
	return WithSectionOptions(ctx, options)
}

func injectPromotedFor(ctx context.Context, resolved []sections.ResolvedSection, enabled bool, source sections.PromoSource) []sections.ResolvedSection {
	if !enabled {
		return dropPromotedSections(resolved)
	}
	if source == nil {
		return resolved
	}
	userID := apimw.GetUserID(ctx)
	profileID := apimw.GetProfileID(ctx)
	if userID <= 0 || profileID == "" {
		return resolved
	}
	for _, s := range resolved {
		if s.SectionType == sections.SectionPromoted {
			return resolved
		}
	}
	viewer := promotions.Viewer{UserID: userID, ProfileID: profileID}
	if scope, ok := access.GetScope(ctx); ok {
		viewer.LibraryIDs = scope.AllowedLibraryIDs
	}
	cards, position, err := source.ActiveHome(ctx, viewer)
	if err != nil {
		slog.ErrorContext(ctx, "resolving home promotions", "component", "api", "error", err)
		return resolved
	}
	if len(cards) == 0 {
		return resolved
	}
	if position < 0 {
		position = 0
	}
	if position > len(resolved) {
		position = len(resolved)
	}
	promoted := sections.ResolvedSection{
		ID:            SystemPromotedSectionID,
		SectionType:   sections.SectionPromoted,
		Title:         "Promoted",
		ItemLimit:     len(cards),
		Position:      position,
		ExtensionData: cards,
	}
	out := make([]sections.ResolvedSection, 0, len(resolved)+1)
	out = append(out, resolved[:position]...)
	out = append(out, promoted)
	out = append(out, resolved[position:]...)
	return out
}

// dropPromotedSections removes SectionPromoted rows; the input is returned
// untouched when it has none.
func dropPromotedSections(resolved []sections.ResolvedSection) []sections.ResolvedSection {
	keep := true
	for _, s := range resolved {
		if s.SectionType == sections.SectionPromoted {
			keep = false
			break
		}
	}
	if keep {
		return resolved
	}
	out := make([]sections.ResolvedSection, 0, len(resolved))
	for _, s := range resolved {
		if s.SectionType != sections.SectionPromoted {
			out = append(out, s)
		}
	}
	return out
}

// sectionItemResponse preserves the native and compatibility card union.
type sectionItemResponse struct {
	ContentID     string   `json:"content_id"`
	PlayContentID string   `json:"play_content_id,omitempty"`
	Type          string   `json:"type"`
	Title         string   `json:"title"`
	SeriesID      string   `json:"series_id,omitempty"`
	SeriesTitle   string   `json:"series_title,omitempty"`
	SeasonNumber  *int     `json:"season_number,omitempty"`
	EpisodeNumber *int     `json:"episode_number,omitempty"`
	Year          int      `json:"year,omitempty"`
	Runtime       int      `json:"runtime,omitempty"`
	Genres        []string `json:"genres"`
	Keywords      []string `json:"keywords"`
	Studios       []string `json:"studios,omitempty"`
	Networks      []string `json:"networks,omitempty"`
	ContentRating string   `json:"content_rating,omitempty"`
	// AdvisoryAge and AdvisorySource carry the item's advisory to the
	// v2 card renderer. json:"-" because /api/v1 is frozen: the fields exist on
	// the Go struct only, and apiv2 emits them under its own names.
	AdvisoryAge       *int                   `json:"-"`
	AdvisorySource    string                 `json:"-"`
	Status            string                 `json:"status"`
	ShowStatus        string                 `json:"show_status,omitempty"`
	RatingIMDB        *float64               `json:"rating_imdb,omitempty"`
	RatingTMDB        *float64               `json:"rating_tmdb,omitempty"`
	RatingRTCritic    *int                   `json:"rating_rt_critic,omitempty"`
	RatingRTAudience  *int                   `json:"rating_rt_audience,omitempty"`
	OriginalLanguage  string                 `json:"original_language,omitempty"`
	Overview          string                 `json:"overview,omitempty"`
	PositionSeconds   *float64               `json:"position_seconds,omitempty"`
	DurationSeconds   *float64               `json:"duration_seconds,omitempty"`
	ProgressUpdatedAt *string                `json:"progress_updated_at,omitempty"`
	PosterURL         string                 `json:"poster_url,omitempty"`
	PosterThumbhash   string                 `json:"poster_thumbhash,omitempty"`
	BackdropURL       string                 `json:"backdrop_url,omitempty"`
	BackdropThumbhash string                 `json:"backdrop_thumbhash,omitempty"`
	LogoURL           string                 `json:"logo_url,omitempty"`
	OverlaySummary    *models.OverlaySummary `json:"overlay_summary,omitempty"`
	Badges            []string               `json:"badges,omitempty"`
	ItemSource        string                 `json:"item_source,omitempty"`
	UserState         *itemUserStateResponse `json:"user_state,omitempty"`
	UpcomingEvent     *upcomingEventResponse `json:"upcoming_event,omitempty"`
	// Promo is the S-2 card variant carried by `promoted` sections; such
	// items have type "promo" and content_id = the promotion id.
	Promo *promotions.Card `json:"promo,omitempty"`
}

// ConfigurePromotions installs owned promotion adapters on the reusable hooks.
// Call once before serving; nil and active sources preserve opt-in gating.
func (h *SectionHandler) ConfigurePromotions(source sections.PromoSource) {
	if h.fetcher != nil {
		sections.InstallPromotions(h.fetcher, source)
	}
	h.HomeTransformers = append(h.HomeTransformers, func(ctx context.Context, resolved []sections.ResolvedSection, options url.Values) []sections.ResolvedSection {
		return injectPromotedFor(ctx, resolved, options.Get(promotedOptInParam) == "1", source)
	})
	h.SectionItemExtensions = append(h.SectionItemExtensions, func(section sections.SectionWithItems) []SectionItemView {
		if section.SectionType != sections.SectionPromoted {
			return nil
		}
		cards := sections.PromotionCards(section.ResolvedSection)
		items := make([]SectionItemView, 0, len(cards))
		for _, card := range cards {
			items = append(items, promoSectionItem(card))
		}
		return items
	})
}
