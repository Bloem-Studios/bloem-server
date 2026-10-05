package promotions

import (
	"context"
	"slices"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

const (
	OperationCandidates = "promotions.candidates"
	OperationDeliver    = "promotions.deliver"
)

// CandidateInput contains organization-scoped rows and dismissal facts supplied
// by the host. Repository order is priority DESC, starts_at, id.
type CandidateInput struct {
	Promotions   []Promotion `json:"promotions"`
	Query        Query       `json:"query"`
	Now          time.Time   `json:"now"`
	DismissedIDs []string    `json:"dismissed_ids"`
}

type CandidateResult struct {
	IDs []string `json:"ids"`
}

// DeliveryInput contains only selected candidates and host-authoritative facts.
// nil LibraryIDs means unrestricted access; an empty slice means no libraries.
type DeliveryInput struct {
	Promotions    []Promotion     `json:"promotions"`
	Viewer        Viewer          `json:"viewer"`
	Role          string          `json:"role"`
	Organizations map[string]bool `json:"organizations"`
}

type DeliveryResult struct {
	Cards        []Card `json:"cards"`
	HomePosition int    `json:"home_position"`
}

// Evaluator separates presentation policy from host repository and authority.
type Evaluator interface {
	Candidates(context.Context, CandidateInput) (CandidateResult, error)
	Deliver(context.Context, DeliveryInput) (DeliveryResult, error)
}

// Engine is the deterministic presentation implementation used by the plugin.
// It has no repository, user-store, environment, or network access.
type Engine struct{}

func (Engine) Candidates(ctx context.Context, in CandidateInput) (CandidateResult, error) {
	out := CandidateResult{IDs: make([]string, 0, len(in.Promotions))}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !IsSurface(in.Query.Surface) {
		return out, invalid("surface must be one of home, detail, pre_playback, in_playback")
	}
	if in.Query.Surface == SurfaceInPlayback && in.Query.ContentID == "" {
		return out, invalid("content_id is required for in_playback")
	}
	dismissed := make(map[string]bool, len(in.DismissedIDs))
	for _, id := range in.DismissedIDs {
		dismissed[id] = true
	}
	for _, p := range in.Promotions {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !slices.Contains(p.Surfaces, in.Query.Surface) || p.StartsAt.After(in.Now) || !in.Now.Before(p.EndsAt) || dismissed[p.ID] || !contentAllowed(p.Placement, in.Query.ContentID) {
			continue
		}
		out.IDs = append(out.IDs, p.ID)
	}
	return out, nil
}

func (Engine) Deliver(ctx context.Context, in DeliveryInput) (DeliveryResult, error) {
	out := DeliveryResult{Cards: make([]Card, 0, len(in.Promotions)), HomePosition: DefaultHomePosition}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	for _, p := range in.Promotions {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !Matches(p.Targeting, in.Viewer, in.Role, in.Organizations) {
			continue
		}
		if len(out.Cards) == 0 && p.Placement.HomePosition != nil {
			out.HomePosition = *p.Placement.HomePosition
		}
		out.Cards = append(out.Cards, p.Card())
	}
	return out, nil
}

// Matches evaluates S-1 targeting against a viewer with the given account
// role and active organization memberships (string UUIDs).
func Matches(t Targeting, v Viewer, role string, orgs map[string]bool) bool {
	switch t.Audience {
	case "", notifications.AudienceAll:
		return true
	case notifications.AudienceRole:
		return role != "" && t.Role == role
	case notifications.AudienceOrganization:
		return orgs[t.OrganizationID]
	case notifications.AudienceLibrary:
		if v.LibraryIDs == nil {
			return true
		}
		for _, id := range v.LibraryIDs {
			if id == t.LibraryID {
				return true
			}
		}
		return false
	case notifications.AudienceExplicit:
		for _, id := range t.UserIDs {
			if id == v.UserID {
				return true
			}
		}
		for _, id := range t.ProfileIDs {
			if id == v.ProfileID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func contentAllowed(p Placement, contentID string) bool {
	if len(p.ContentIDs) == 0 {
		return true
	}
	if contentID == "" {
		return false
	}
	for _, id := range p.ContentIDs {
		if id == contentID {
			return true
		}
	}
	return false
}

// Card projects the promotion onto the client shape.
func (p Promotion) Card() Card {
	duration := p.Placement.DurationSeconds
	if duration == 0 && p.Placement.PlaybackStyle != "" {
		duration = 10
	}
	return Card{
		DurationSeconds: duration,
		PlaybackStyle:   p.Placement.PlaybackStyle,
		VideoURL:        p.Placement.VideoURL,
		ExpiresAt:       p.EndsAt,
		ID:              p.ID,
		Kicker:          p.Kicker,
		Headline:        p.Headline,
		Subtitle:        p.Subtitle,
		ImageURL:        p.ImageURL,
		Deeplink:        p.Deeplink,
		CTA:             p.CTA,
		Dismissible:     p.Dismissible,
	}
}
