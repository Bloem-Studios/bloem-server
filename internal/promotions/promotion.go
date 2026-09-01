// Package promotions implements S-2 of docs/specs/client-engagement.md: admin
// authored promotional cards delivered on the home screen (as a `promoted`
// section), on item detail pages and before playback. A card is 16:9 artwork
// with a kicker, a headline and an optional subtitle, deeplink and CTA.
//
// Pre-playback contract (amendment 3): the client always keeps "continue to
// content" as the default action. The server cannot request a timer or a
// forced wait — no such fields exist anywhere in this package or its tables.
package promotions

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// ErrInvalid wraps every validation failure; the joined message names the field.
var ErrInvalid = errors.New("promotions: invalid promotion")

// ErrNotFound is returned for unknown promotion ids.
var ErrNotFound = errors.New("promotions: promotion not found")

// Surfaces vocabulary: where a card may be delivered.
const (
	SurfaceHome        = "home"
	SurfaceDetail      = "detail"
	SurfacePrePlayback = "pre_playback"
)

// Surfaces lists the delivery surfaces in capability order.
var Surfaces = []string{SurfaceHome, SurfaceDetail, SurfacePrePlayback}

// DismissalSurfacePrefix namespaces promo dismissals inside the per-profile
// home dismissal store ("promo:home", "promo:detail", "promo:pre_playback").
const DismissalSurfacePrefix = "promo:"

// Targeting reuses the S-1 announcement audience vocabulary
// (all | role | organization | library | explicit).
type Targeting = notifications.AnnouncementTargeting

const (
	maxKickerLen   = 40
	maxHeadlineLen = 120
	maxSubtitleLen = 200
	maxCTALabelLen = 40
	maxContentIDs  = 64
	// aspectTolerance is the relative deviation from 16:9 accepted when the
	// artwork's dimensions are declared (±1%).
	aspectTolerance = 0.01
)

// IsSurface reports whether s is a delivery surface.
func IsSurface(s string) bool {
	return s == SurfaceHome || s == SurfaceDetail || s == SurfacePrePlayback
}

// DismissalSurface maps a delivery surface to its dismissal-store surface.
func DismissalSurface(surface string) string { return DismissalSurfacePrefix + surface }

// IsDismissalSurface reports whether s is a promo dismissal surface
// ("promo:<surface>").
func IsDismissalSurface(s string) bool {
	return strings.HasPrefix(s, DismissalSurfacePrefix) && IsSurface(strings.TrimPrefix(s, DismissalSurfacePrefix))
}

// CTA is the optional call-to-action button on a card.
type CTA struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Placement carries free-form placement hints. home_position is the index
// at which the synthetic `promoted` section is inserted into the home layout
// (default 1: after the first row); detail_slot names a client-defined slot on
// the detail page; content_ids restricts detail / pre-playback delivery to
// the listed catalog items.
type Placement struct {
	HomePosition *int     `json:"home_position,omitempty"`
	DetailSlot   string   `json:"detail_slot,omitempty"`
	ContentIDs   []string `json:"content_ids,omitempty"`
}

// Promotion is a stored campaign card (admin view).
type Promotion struct {
	ID             string     `json:"id"`
	OrganizationID *uuid.UUID `json:"organization_id"`
	Surfaces       []string   `json:"surfaces"`
	Placement      Placement  `json:"placement"`
	Kicker         string     `json:"kicker"`
	Headline       string     `json:"headline"`
	Subtitle       string     `json:"subtitle"`
	ImageURL       string     `json:"image_url"`
	ImageWidth     *int       `json:"image_width,omitempty"`
	ImageHeight    *int       `json:"image_height,omitempty"`
	Deeplink       string     `json:"deeplink"`
	CTA            *CTA       `json:"cta"`
	Priority       int        `json:"priority"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	Targeting      Targeting  `json:"targeting"`
	Dismissible    bool       `json:"dismissible"`
	CreatedBy      int        `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Card is the client-facing shape: the same on the home `promoted` section
// items and on GET /promotions. It carries no timer or wait field.
type Card struct {
	ID          string `json:"id"`
	Kicker      string `json:"kicker"`
	Headline    string `json:"headline"`
	Subtitle    string `json:"subtitle,omitempty"`
	ImageURL    string `json:"image_url"`
	Deeplink    string `json:"deeplink,omitempty"`
	CTA         *CTA   `json:"cta,omitempty"`
	Dismissible bool   `json:"dismissible"`
}

// Card projects the promotion onto the client shape.
func (p Promotion) Card() Card {
	return Card{
		ID:          p.ID,
		Kicker:      p.Kicker,
		Headline:    p.Headline,
		Subtitle:    p.Subtitle,
		ImageURL:    p.ImageURL,
		Deeplink:    p.Deeplink,
		CTA:         p.CTA,
		Dismissible: p.Dismissible,
	}
}

// Input is the admin create/update body. dismissible defaults to true and
// priority to 0; organization_id null means deployment-wide.
type Input struct {
	OrganizationID *uuid.UUID `json:"organization_id"`
	Surfaces       []string   `json:"surfaces"`
	Placement      Placement  `json:"placement"`
	Kicker         string     `json:"kicker"`
	Headline       string     `json:"headline"`
	Subtitle       string     `json:"subtitle"`
	ImageURL       string     `json:"image_url"`
	ImageWidth     *int       `json:"image_width"`
	ImageHeight    *int       `json:"image_height"`
	Deeplink       string     `json:"deeplink"`
	CTA            *CTA       `json:"cta"`
	Priority       int        `json:"priority"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         time.Time  `json:"ends_at"`
	Targeting      Targeting  `json:"targeting"`
	Dismissible    *bool      `json:"dismissible"`
}

// Normalized is a validated Input with defaults applied.
type Normalized struct {
	OrganizationID *uuid.UUID
	Surfaces       []string
	Placement      Placement
	Kicker         string
	Headline       string
	Subtitle       string
	ImageURL       string
	ImageWidth     *int
	ImageHeight    *int
	Deeplink       string
	CTA            *CTA
	Priority       int
	StartsAt       time.Time
	EndsAt         time.Time
	Targeting      Targeting
	Dismissible    bool
}

func invalid(format string, args ...any) error {
	return errors.Join(ErrInvalid, fmt.Errorf(format, args...))
}

// Normalize validates an Input and applies defaults. Rules: surfaces come
// from {home, detail, pre_playback} (at least one, deduplicated); headline is
// required; image_url is an https:// URL or a server asset path (the S-3
// ambience asset validator); declared image dimensions must be 16:9 within
// ±1%; deeplink and cta.url follow the S-1 link rule (https, bloem://, app
// path); starts_at < ends_at; targeting follows the S-1 audience vocabulary.
func Normalize(in Input) (Normalized, error) {
	out := Normalized{OrganizationID: in.OrganizationID, Priority: in.Priority, Dismissible: true}

	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return out, err
	}
	out.Surfaces = surfaces

	out.Kicker = strings.TrimSpace(in.Kicker)
	if len(out.Kicker) > maxKickerLen {
		return out, invalid("kicker must be at most %d characters", maxKickerLen)
	}
	out.Headline = strings.TrimSpace(in.Headline)
	if out.Headline == "" {
		return out, invalid("headline is required")
	}
	if len(out.Headline) > maxHeadlineLen {
		return out, invalid("headline must be at most %d characters", maxHeadlineLen)
	}
	out.Subtitle = strings.TrimSpace(in.Subtitle)
	if len(out.Subtitle) > maxSubtitleLen {
		return out, invalid("subtitle must be at most %d characters", maxSubtitleLen)
	}

	out.ImageURL = strings.TrimSpace(in.ImageURL)
	if out.ImageURL == "" {
		return out, invalid("image_url is required")
	}
	if !ambience.IsAssetURL(out.ImageURL) {
		return out, invalid("image_url must be an https:// URL or a server asset path")
	}
	width, height, err := normalizeDimensions(in.ImageWidth, in.ImageHeight)
	if err != nil {
		return out, err
	}
	out.ImageWidth, out.ImageHeight = width, height

	out.Deeplink = strings.TrimSpace(in.Deeplink)
	if out.Deeplink != "" && !notifications.IsAppLink(out.Deeplink) {
		return out, invalid("deeplink must be an https URL, a bloem:// deeplink, or an app path")
	}
	if in.CTA != nil {
		cta := CTA{Label: strings.TrimSpace(in.CTA.Label), URL: strings.TrimSpace(in.CTA.URL)}
		if cta.Label == "" || len(cta.Label) > maxCTALabelLen {
			return out, invalid("cta.label is required and at most %d characters", maxCTALabelLen)
		}
		if !notifications.IsAppLink(cta.URL) {
			return out, invalid("cta.url must be an https URL, a bloem:// deeplink, or an app path")
		}
		out.CTA = &cta
	}

	if in.StartsAt.IsZero() || in.EndsAt.IsZero() {
		return out, invalid("starts_at and ends_at are required")
	}
	if !in.StartsAt.Before(in.EndsAt) {
		return out, invalid("starts_at must be before ends_at")
	}
	out.StartsAt, out.EndsAt = in.StartsAt.UTC(), in.EndsAt.UTC()

	placement, err := normalizePlacement(in.Placement)
	if err != nil {
		return out, err
	}
	out.Placement = placement

	targeting, err := notifications.ValidateTargeting(in.Targeting)
	if err != nil {
		return out, invalid("targeting: %v", err)
	}
	out.Targeting = targeting

	if in.Dismissible != nil {
		out.Dismissible = *in.Dismissible
	}
	return out, nil
}

func normalizeSurfaces(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, invalid("surfaces must list at least one of home, detail, pre_playback")
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if !IsSurface(s) {
			return nil, invalid("surfaces must be one or more of home, detail, pre_playback")
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func normalizeDimensions(width, height *int) (*int, *int, error) {
	if width == nil && height == nil {
		return nil, nil, nil
	}
	if width == nil || height == nil {
		return nil, nil, invalid("image_width and image_height must be given together")
	}
	if *width <= 0 || *height <= 0 {
		return nil, nil, invalid("image_width and image_height must be positive")
	}
	if !IsSixteenByNine(*width, *height) {
		return nil, nil, invalid("image must be 16:9 artwork (declared %dx%d)", *width, *height)
	}
	w, h := *width, *height
	return &w, &h, nil
}

// IsSixteenByNine reports whether width:height is 16:9 within ±1%.
func IsSixteenByNine(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	ratio := float64(width) / float64(height)
	const target = 16.0 / 9.0
	return math.Abs(ratio-target)/target <= aspectTolerance
}

func normalizePlacement(in Placement) (Placement, error) {
	out := Placement{DetailSlot: strings.TrimSpace(in.DetailSlot)}
	if in.HomePosition != nil {
		if *in.HomePosition < 0 {
			return out, invalid("placement.home_position must be zero or positive")
		}
		pos := *in.HomePosition
		out.HomePosition = &pos
	}
	if len(in.ContentIDs) > maxContentIDs {
		return out, invalid("placement.content_ids holds at most %d entries", maxContentIDs)
	}
	for _, id := range in.ContentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return out, invalid("placement.content_ids entries must not be empty")
		}
		out.ContentIDs = append(out.ContentIDs, id)
	}
	return out, nil
}
