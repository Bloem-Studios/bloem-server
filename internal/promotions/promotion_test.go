package promotions

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

var (
	promoStart = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	promoEnd   = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
)

func validInput() Input {
	return Input{
		Surfaces: []string{"home", "detail"},
		Kicker:   "New this week",
		Headline: "The Bloem Winter Collection",
		Subtitle: "Ten films, one long weekend.",
		ImageURL: "https://cdn.example/winter-16x9.jpg",
		Deeplink: "bloem://collection/winter",
		CTA:      &CTA{Label: "Browse", URL: "/collections/winter"},
		Priority: 5,
		StartsAt: promoStart,
		EndsAt:   promoEnd,
	}
}

func TestNormalizeAppliesDefaults(t *testing.T) {
	n, err := Normalize(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if !n.Dismissible || n.Priority != 5 || n.Targeting.Audience != notifications.AudienceAll || len(n.Surfaces) != 2 {
		t.Fatalf("unexpected normalized: %+v", n)
	}
	f := false
	in := validInput()
	in.Dismissible = &f
	in.Surfaces = []string{"Home", "home", " pre_playback "}
	n, err = Normalize(in)
	if err != nil || n.Dismissible || strings.Join(n.Surfaces, ",") != "home,pre_playback" {
		t.Fatalf("dismissible/surfaces: %+v %v", n, err)
	}
}

func TestNormalizeRejectsInvalidInput(t *testing.T) {
	w, h := 1920, 1080
	badW, badH := 1000, 1500
	edgeW, edgeH := 1600, 900
	offW, offH := 1600, 920 // 1.739 vs 1.778 → 2.2% off
	cases := map[string]func(*Input){
		"no surfaces":         func(i *Input) { i.Surfaces = nil },
		"unknown surface":     func(i *Input) { i.Surfaces = []string{"login"} },
		"missing headline":    func(i *Input) { i.Headline = " " },
		"missing image":       func(i *Input) { i.ImageURL = "" },
		"http image":          func(i *Input) { i.ImageURL = "http://cdn.example/a.jpg" },
		"data image":          func(i *Input) { i.ImageURL = "data:image/png;base64,AAAA" },
		"foreign app path":    func(i *Input) { i.ImageURL = "/api/v1/items/x/poster" },
		"poster aspect":       func(i *Input) { i.ImageWidth, i.ImageHeight = &badW, &badH },
		"just off 16:9":       func(i *Input) { i.ImageWidth, i.ImageHeight = &offW, &offH },
		"width only":          func(i *Input) { i.ImageWidth = &w },
		"javascript deeplink": func(i *Input) { i.Deeplink = "javascript:alert(1)" },
		"http deeplink":       func(i *Input) { i.Deeplink = "http://example.com" },
		"protocol relative":   func(i *Input) { i.Deeplink = "//example.com/x" },
		"cta without label":   func(i *Input) { i.CTA = &CTA{URL: "https://example.com"} },
		"cta bad url":         func(i *Input) { i.CTA = &CTA{Label: "Go", URL: "ftp://example.com"} },
		"window order":        func(i *Input) { i.StartsAt, i.EndsAt = promoEnd, promoStart },
		"window missing":      func(i *Input) { i.EndsAt = time.Time{} },
		"bad audience":        func(i *Input) { i.Targeting = Targeting{Audience: "everyone"} },
		"role missing":        func(i *Input) { i.Targeting = Targeting{Audience: "role"} },
		"negative position":   func(i *Input) { p := -1; i.Placement.HomePosition = &p },
		"empty content id":    func(i *Input) { i.Placement.ContentIDs = []string{""} },
	}
	for name, mutate := range cases {
		in := validInput()
		mutate(&in)
		if _, err := Normalize(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
	in := validInput()
	in.ImageWidth, in.ImageHeight = &w, &h
	if _, err := Normalize(in); err != nil {
		t.Fatalf("1920x1080 must pass: %v", err)
	}
	in.ImageWidth, in.ImageHeight = &edgeW, &edgeH
	if _, err := Normalize(in); err != nil {
		t.Fatalf("1600x900 must pass: %v", err)
	}
	in.ImageWidth, in.ImageHeight = &w, &h
	in.ImageURL = "/api/v1/ambience/assets/0123456789abcdef.png"
	if _, err := Normalize(in); err != nil {
		t.Fatalf("S-3 asset path must pass: %v", err)
	}
}

func TestIsSixteenByNineTolerance(t *testing.T) {
	if !IsSixteenByNine(1280, 720) || !IsSixteenByNine(1366, 768) || IsSixteenByNine(1000, 1500) || IsSixteenByNine(4, 3) || IsSixteenByNine(0, 9) {
		t.Fatal("tolerance mismatch")
	}
}

func TestTargetingSideFieldsAreCanonicalized(t *testing.T) {
	in := validInput()
	in.Targeting = Targeting{Audience: " ROLE ", Role: "Admin", LibraryID: 4}
	n, err := Normalize(in)
	if err != nil || n.Targeting.Audience != "role" || n.Targeting.Role != "admin" || n.Targeting.LibraryID != 0 {
		t.Fatalf("targeting: %+v %v", n.Targeting, err)
	}
}

// The pre-playback contract (amendment 3): the card carries no timer / wait
// fields; the client always keeps "continue to content" as the default action.
func TestCardWireShapeHasNoTimerFields(t *testing.T) {
	n, err := Normalize(validInput())
	if err != nil {
		t.Fatal(err)
	}
	p := Promotion{ID: "01PROMO", Kicker: n.Kicker, Headline: n.Headline, Subtitle: n.Subtitle, ImageURL: n.ImageURL, Deeplink: n.Deeplink, CTA: n.CTA, Dismissible: n.Dismissible}
	raw, err := json.Marshal(p.Card())
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"id": true, "kicker": true, "headline": true, "subtitle": true, "image_url": true, "deeplink": true, "cta": true, "dismissible": true, "expires_at": true, "duration_seconds": true, "playback_style": true, "video_url": true}
	for k := range keys {
		if !allowed[k] {
			t.Fatalf("unexpected card key %q in %s", k, raw)
		}
		lower := strings.ToLower(k)
		for _, banned := range []string{"timer", "wait", "delay", "skip", "countdown", "duration"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("card must never carry a %q field: %s", banned, raw)
			}
		}
	}
	t.Logf("promo card wire shape: %s", raw)
}

func TestDismissalSurfaces(t *testing.T) {
	if DismissalSurface("home") != "promo:home" || !IsDismissalSurface("promo:pre_playback") || IsDismissalSurface("promo:login") || IsDismissalSurface("home") {
		t.Fatal("dismissal surface mapping")
	}
}

func TestPlaybackCardExpiry(t *testing.T) {
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	card := (Promotion{EndsAt: end, Placement: Placement{PlaybackStyle: "card"}}).Card()
	if !card.ExpiresAt.Equal(end) || card.PlaybackStyle != "card" {
		t.Fatalf("missing featured delivery metadata: %+v", card)
	}
	if _, err := normalizePlacement(Placement{PlaybackStyle: "popup"}); err == nil {
		t.Fatal("accepted unknown placement")
	}
}
