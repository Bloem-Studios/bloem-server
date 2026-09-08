package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type initialSessionResolution struct {
	card  *playback.RecipeCard
	bound bool
	err   error
}

func (r initialSessionResolution) ResolveCurrentSession(context.Context, string) (*playback.RecipeCard, bool, error) {
	return r.card, r.bound, r.err
}

type initialLegacyLookupProbe struct{ calls int }

func (r *initialLegacyLookupProbe) Get(context.Context, string) (*playback.RecipeCard, bool) {
	r.calls++
	return &playback.RecipeCard{SessionID: "legacy"}, true
}

func TestInitialNodeGrantLookupNeverFallsBackForBoundSessions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result initialSessionResolution
		legacy bool
		found  bool
	}{
		{"no-bound-row", initialSessionResolution{}, true, true},
		{"bound-active", initialSessionResolution{card: &playback.RecipeCard{SessionID: "bound"}, bound: true}, false, true},
		{"bound-unavailable", initialSessionResolution{bound: true, err: playback.ErrStaleAttemptAuthorityV3}, false, false},
		{"bound-missing-recipe", initialSessionResolution{bound: true}, false, false},
		{"lookup-failed", initialSessionResolution{err: errors.New("database unavailable")}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := new(initialLegacyLookupProbe)
			lookup := initialNodeGrantLookup{runtime: tc.result, legacy: legacy}
			card, ok := lookup.Get(t.Context(), "session")
			if ok != tc.found || (legacy.calls == 1) != tc.legacy {
				t.Fatalf("found=%v legacy calls=%d", ok, legacy.calls)
			}
			if ok && card == nil {
				t.Fatal("found nil recipe")
			}
		})
	}
}
