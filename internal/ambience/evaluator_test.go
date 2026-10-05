package ambience

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func seasonalCandidate() Wire {
	return Wire{ID: "winter", EffectID: "snow", Window: Window{StartsAt: instant("2026-11-30T23:00:00Z"), EndsAt: instant("2026-12-31T23:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}, Intensity: 0.25, Surfaces: []string{SurfaceHome}, Assets: Assets{BannerURL: "https://cdn.example/banner.png", Sprites: []string{AssetURLBase + "0123456789abcdef.webp"}}}
}

func TestEngineSeasonalBoundariesAndProjection(t *testing.T) {
	candidate := seasonalCandidate()
	for _, tc := range []struct {
		at     string
		active bool
	}{
		{"2025-12-15T00:00:00Z", false}, {"2029-11-30T22:59:59Z", false},
		{"2029-11-30T23:00:00Z", true}, {"2029-12-31T22:59:59Z", true}, {"2029-12-31T23:00:00Z", false},
	} {
		t.Run(tc.at, func(t *testing.T) {
			got, err := (Engine{}).Evaluate(t.Context(), EvaluationRequest{Now: instant(tc.at), Candidates: []Wire{candidate}})
			if err != nil {
				t.Fatal(err)
			}
			if (len(got.Packs) == 1) != tc.active {
				t.Fatalf("packs = %+v", got.Packs)
			}
			if tc.active {
				want := candidate
				want.Window = Window{StartsAt: instant("2029-11-30T23:00:00Z"), EndsAt: instant("2029-12-31T23:00:00Z")}
				if !reflect.DeepEqual(got.Packs[0], want) {
					t.Fatalf("pack = %+v, want %+v", got.Packs[0], want)
				}
			}
		})
	}
	if !candidate.Window.RepeatYearly {
		t.Fatal("evaluation mutated stored recurrence")
	}
}

func TestEngineCrossYearLeapAndDST(t *testing.T) {
	for _, tc := range []struct {
		name       string
		window     Window
		now        string
		start, end string
	}{
		{"cross year", Window{StartsAt: instant("2026-12-01T00:00:00Z"), EndsAt: instant("2027-01-07T00:00:00Z"), RepeatYearly: true, Timezone: "UTC"}, "2029-01-01T00:00:00Z", "2028-12-01T00:00:00Z", "2029-01-07T00:00:00Z"},
		{"leap date skipped", Window{StartsAt: instant("2028-02-29T00:00:00Z"), EndsAt: instant("2028-03-02T00:00:00Z"), RepeatYearly: true, Timezone: "UTC"}, "2029-03-01T00:00:00Z", "", ""},
		{"DST gap skipped", Window{StartsAt: instant("2026-03-28T01:30:00Z"), EndsAt: instant("2026-03-29T08:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}, "2027-03-28T12:00:00Z", "", ""},
		{"timezone DST preserved", Window{StartsAt: instant("2026-03-01T09:00:00Z"), EndsAt: instant("2026-04-01T08:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}, "2027-03-15T00:00:00Z", "2027-03-01T09:00:00Z", "2027-04-01T08:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (Engine{}).Evaluate(t.Context(), EvaluationRequest{Now: instant(tc.now), Candidates: []Wire{{ID: tc.name, Window: tc.window}}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.start == "" {
				if len(got.Packs) != 0 {
					t.Fatal(got)
				}
				return
			}
			if len(got.Packs) != 1 || got.Packs[0].Window.StartsAt != instant(tc.start) || got.Packs[0].Window.EndsAt != instant(tc.end) {
				t.Fatal(got)
			}
		})
	}
}

func TestEnginePreservesCandidateOrderAndEmptyArray(t *testing.T) {
	first := seasonalCandidate()
	second := first
	second.ID = "second"
	got, err := (Engine{}).Evaluate(t.Context(), EvaluationRequest{Now: instant("2029-12-15T00:00:00Z"), Candidates: []Wire{second, first}})
	if err != nil || len(got.Packs) != 2 || got.Packs[0].ID != second.ID || got.Packs[1].ID != first.ID {
		t.Fatal(got, err)
	}
	got, err = (Engine{}).Evaluate(t.Context(), EvaluationRequest{Now: instant("2029-12-15T00:00:00Z")})
	if err != nil || got.Packs == nil || len(got.Packs) != 0 {
		t.Fatal(got, err)
	}
}

func TestEngineRejectsMissingInstantAndCancellation(t *testing.T) {
	if _, err := (Engine{}).Evaluate(t.Context(), EvaluationRequest{}); err == nil {
		t.Fatal("missing instant accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (Engine{}).Evaluate(ctx, EvaluationRequest{Now: time.Now()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestEngineOneOffHalfOpenWindow(t *testing.T) {
	window := Window{StartsAt: instant("2029-12-01T00:00:00Z"), EndsAt: instant("2030-01-01T00:00:00Z")}
	for _, tc := range []struct {
		now    time.Time
		active bool
	}{{window.StartsAt.Add(-time.Nanosecond), false}, {window.StartsAt, true}, {window.EndsAt.Add(-time.Nanosecond), true}, {window.EndsAt, false}} {
		result, err := (Engine{}).Evaluate(t.Context(), EvaluationRequest{Now: tc.now, Candidates: []Wire{{ID: "one-off", Window: window}}})
		if err != nil || (len(result.Packs) == 1) != tc.active {
			t.Fatal(result, err)
		}
		if tc.active && result.Packs[0].Window != window {
			t.Fatal("one-off window altered", result)
		}
	}
}
