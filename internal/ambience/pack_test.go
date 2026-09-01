package ambience

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validInput() Input {
	i := 0.5
	return Input{
		EffectID:  "halloween-2026",
		Window:    Window{StartsAt: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		Intensity: &i,
		Surfaces:  []string{"home", "login", "home"},
		Assets:    Assets{BannerURL: "https://cdn.example/halloween/banner.png", Sprites: []string{"https://cdn.example/halloween/pumpkin.png", AssetURLBase + "0123456789abcdef.webp"}},
	}
}

func TestNormalizeAppliesDefaultsAndDedupes(t *testing.T) {
	in := validInput()
	in.Intensity = nil
	in.Surfaces = nil
	got, err := Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Intensity != 1.0 || len(got.Surfaces) != 1 || got.Surfaces[0] != SurfaceAll {
		t.Fatalf("defaults not applied: %+v", got)
	}
	got, err = Normalize(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Surfaces, ",") != "home,login" || got.Intensity != 0.5 || len(got.Assets.Sprites) != 2 {
		t.Fatalf("unexpected normalized pack: %+v", got)
	}
	in = validInput()
	in.Surfaces = []string{"home", "all"}
	got, _ = Normalize(in)
	if strings.Join(got.Surfaces, ",") != "all" {
		t.Fatalf("all must absorb other surfaces: %v", got.Surfaces)
	}
}

func TestNormalizeRejectsInvalidFields(t *testing.T) {
	neg, over := -0.1, 1.01
	cases := map[string]func(*Input){
		"missing effect_id":    func(i *Input) { i.EffectID = " " },
		"uppercase effect_id":  func(i *Input) { i.EffectID = "Snow" },
		"long effect_id":       func(i *Input) { i.EffectID = strings.Repeat("a", 65) },
		"missing window":       func(i *Input) { i.Window = Window{} },
		"reversed window":      func(i *Input) { i.Window.StartsAt, i.Window.EndsAt = i.Window.EndsAt, i.Window.StartsAt },
		"empty window":         func(i *Input) { i.Window.EndsAt = i.Window.StartsAt },
		"negative intensity":   func(i *Input) { i.Intensity = &neg },
		"intensity above one":  func(i *Input) { i.Intensity = &over },
		"unknown surface":      func(i *Input) { i.Surfaces = []string{"tv"} },
		"http banner":          func(i *Input) { i.Assets.BannerURL = "http://cdn.example/b.png" },
		"javascript banner":    func(i *Input) { i.Assets.BannerURL = "javascript:alert(1)" },
		"protocol-relative":    func(i *Input) { i.Assets.BannerURL = "//cdn.example/b.png" },
		"foreign app path":     func(i *Input) { i.Assets.BannerURL = "/api/v1/branding/assets/mark" },
		"bad server asset ref": func(i *Input) { i.Assets.BannerURL = AssetURLBase + "../etc/passwd" },
		"http sprite":          func(i *Input) { i.Assets.Sprites = []string{"http://cdn.example/s.png"} },
		"too many sprites":     func(i *Input) { i.Assets.Sprites = make([]string, 33) },
	}
	for name, mutate := range cases {
		in := validInput()
		mutate(&in)
		if _, err := Normalize(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestWindowContainsIsHalfOpen(t *testing.T) {
	w := Window{StartsAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2027, 1, 7, 0, 0, 0, 0, time.UTC)}
	if !w.Contains(w.StartsAt) || w.Contains(w.EndsAt) || w.Contains(w.StartsAt.Add(-time.Nanosecond)) || !w.Contains(w.EndsAt.Add(-time.Nanosecond)) {
		t.Fatal("window must include starts_at and exclude ends_at")
	}
}
