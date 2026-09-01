// Package ambience implements S-3 of docs/specs/client-engagement.md: a
// registry of seasonal presentation packs. A pack is a window in time during
// which clients render an effect (client-drawn effects such as snow, or the
// generic artwork renderer fed by the pack's banner/sprite assets). The server
// stores one row per season and evaluates the schedule; it has no per-season
// logic. Everything here is additive and capability-gated on the wire.
package ambience

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrInvalid wraps every validation failure; the joined message names the field.
var ErrInvalid = errors.New("ambience: invalid pack")

// ErrNotFound is returned for unknown pack ids.
var ErrNotFound = errors.New("ambience: pack not found")

// Surfaces vocabulary: where the client applies the effect.
const (
	SurfaceAll   = "all"
	SurfaceHome  = "home"
	SurfaceLogin = "login"
)

// AssetURLBase is the public path prefix under which server-stored ambience
// artwork is served (see Service.ServeAsset). Pack asset URLs must be https://
// absolute URLs or paths under this prefix.
const AssetURLBase = "/api/v1/ambience/assets/"

const (
	maxEffectIDLen = 64
	maxSprites     = 32
	maxAssetURLLen = 2048
)

var (
	effectIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	validSurfaces   = map[string]bool{SurfaceAll: true, SurfaceHome: true, SurfaceLogin: true}
)

// Window is the inclusive-start, exclusive-end UTC interval a pack is active.
type Window struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// Contains reports whether now falls inside the window (starts_at <= now < ends_at).
func (w Window) Contains(now time.Time) bool {
	return !now.Before(w.StartsAt) && now.Before(w.EndsAt)
}

// Assets is the optional artwork shipped with a pack. Clients that know the
// effect_id may ignore it; unknown effect ids with assets go to the generic
// artwork renderer, unknown ids without assets are ignored.
type Assets struct {
	BannerURL string   `json:"banner_url,omitempty"`
	Sprites   []string `json:"sprites,omitempty"`
}

// Pack is a stored registry entry (admin view).
type Pack struct {
	ID             string     `json:"id"`
	EffectID       string     `json:"effect_id"`
	Window         Window     `json:"window"`
	Intensity      float64    `json:"intensity"`
	Surfaces       []string   `json:"surfaces"`
	Assets         Assets     `json:"assets"`
	OrganizationID *uuid.UUID `json:"organization_id"`
	CreatedBy      int        `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Wire is the client-facing shape emitted in the `ambience` block of the
// branding and capability payloads: the pack minus its audit columns.
type Wire struct {
	ID        string   `json:"id"`
	EffectID  string   `json:"effect_id"`
	Window    Window   `json:"window"`
	Intensity float64  `json:"intensity"`
	Surfaces  []string `json:"surfaces"`
	Assets    Assets   `json:"assets"`
}

// Wire projects the pack onto the client shape.
func (p Pack) Wire() Wire {
	surfaces := p.Surfaces
	if surfaces == nil {
		surfaces = []string{}
	}
	return Wire{ID: p.ID, EffectID: p.EffectID, Window: p.Window, Intensity: p.Intensity, Surfaces: surfaces, Assets: p.Assets}
}

// Input is the admin create/update body. Intensity defaults to 1.0 and
// surfaces to ["all"] when omitted; organization_id null means deployment-wide.
type Input struct {
	EffectID       string     `json:"effect_id"`
	Window         Window     `json:"window"`
	Intensity      *float64   `json:"intensity"`
	Surfaces       []string   `json:"surfaces"`
	Assets         Assets     `json:"assets"`
	OrganizationID *uuid.UUID `json:"organization_id"`
}

// Normalized is a validated Input with defaults applied.
type Normalized struct {
	EffectID       string
	Window         Window
	Intensity      float64
	Surfaces       []string
	Assets         Assets
	OrganizationID *uuid.UUID
}

func invalid(format string, args ...any) error {
	return errors.Join(ErrInvalid, fmt.Errorf(format, args...))
}

// Normalize validates an Input and applies defaults. Rules: effect_id is a
// required lowercase slug (max 64 chars); the window needs both bounds with
// starts_at < ends_at; intensity is within [0, 1]; surfaces come from the
// {all, home, login} vocabulary ("all" absorbs the rest); asset URLs are
// https:// absolute URLs or server-served paths under AssetURLBase.
func Normalize(in Input) (Normalized, error) {
	out := Normalized{OrganizationID: in.OrganizationID}

	out.EffectID = strings.TrimSpace(in.EffectID)
	if out.EffectID == "" {
		return out, invalid("effect_id is required")
	}
	if len(out.EffectID) > maxEffectIDLen || !effectIDPattern.MatchString(out.EffectID) {
		return out, invalid("effect_id must be a lowercase slug of at most %d characters", maxEffectIDLen)
	}

	if in.Window.StartsAt.IsZero() || in.Window.EndsAt.IsZero() {
		return out, invalid("window.starts_at and window.ends_at are required")
	}
	if !in.Window.StartsAt.Before(in.Window.EndsAt) {
		return out, invalid("window.starts_at must be before window.ends_at")
	}
	out.Window = Window{StartsAt: in.Window.StartsAt.UTC(), EndsAt: in.Window.EndsAt.UTC()}

	out.Intensity = 1.0
	if in.Intensity != nil {
		if *in.Intensity < 0 || *in.Intensity > 1 || *in.Intensity != *in.Intensity {
			return out, invalid("intensity must be between 0.0 and 1.0")
		}
		out.Intensity = *in.Intensity
	}

	surfaces, err := normalizeSurfaces(in.Surfaces)
	if err != nil {
		return out, err
	}
	out.Surfaces = surfaces

	assets, err := NormalizeAssets(in.Assets)
	if err != nil {
		return out, err
	}
	out.Assets = assets
	return out, nil
}

func normalizeSurfaces(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{SurfaceAll}, nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if !validSurfaces[s] {
			return nil, invalid("surfaces must be one or more of all, home, login")
		}
		if s == SurfaceAll {
			return []string{SurfaceAll}, nil
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

// NormalizeAssets validates the artwork references of a pack.
func NormalizeAssets(in Assets) (Assets, error) {
	out := Assets{BannerURL: strings.TrimSpace(in.BannerURL)}
	if out.BannerURL != "" && !IsAssetURL(out.BannerURL) {
		return out, invalid("assets.banner_url must be an https:// URL or a server asset path")
	}
	if len(in.Sprites) > maxSprites {
		return out, invalid("assets.sprites holds at most %d entries", maxSprites)
	}
	for _, s := range in.Sprites {
		s = strings.TrimSpace(s)
		if !IsAssetURL(s) {
			return out, invalid("assets.sprites entries must be https:// URLs or server asset paths")
		}
		out.Sprites = append(out.Sprites, s)
	}
	return out, nil
}

// IsAssetURL reports whether s is an acceptable artwork reference: an
// absolute https:// URL with a host, or a server-served path under
// AssetURLBase. Everything else (http:, data:, javascript:, protocol-relative,
// arbitrary app paths) is rejected.
func IsAssetURL(s string) bool {
	if s == "" || len(s) > maxAssetURLLen {
		return false
	}
	if strings.HasPrefix(s, AssetURLBase) {
		return assetRefPattern.MatchString(strings.TrimPrefix(s, AssetURLBase))
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != ""
}
