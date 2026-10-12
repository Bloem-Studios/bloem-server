package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
)

type managedCalendarResolverFixture struct {
	scope access.Scope
	err   error
}

func (f managedCalendarResolverFixture) Resolve(context.Context, access.ResolveInput) (access.Scope, error) {
	return f.scope, f.err
}

func TestManagedCalendarScopeFailsClosed(t *testing.T) {
	p := managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}
	for _, scope := range []access.Scope{
		{UserID: 42, ProfileID: "profile", ProfileVerified: false},
		{UserID: 42, ProfileID: "profile", ProfileVerified: true, PreferencesDegraded: true},
		{UserID: 42, ProfileID: "other", ProfileVerified: true},
		{UserID: 41, ProfileID: "profile", ProfileVerified: true},
	} {
		source := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: scope}}
		if _, err := source.Authority(t.Context(), p); !errors.Is(err, managedtracking.ErrAuthority) {
			t.Fatalf("unverified/degraded/foreign scope accepted: %v %v", scope, err)
		}
	}
}
func TestManagedCalendarFingerprintTracksVisibility(t *testing.T) {
	p := managedtracking.Profile{AccountID: "42", ProfileID: "profile", AccountActive: true, Active: true}
	first := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, AllowedLibraryIDs: []int{1, 2}, DisabledLibraryIDs: []int{3}}}}
	a, err := first.Authority(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	reordered := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, AllowedLibraryIDs: []int{2, 1}, DisabledLibraryIDs: []int{3}}}}
	b, err := reordered.Authority(t.Context(), p)
	if err != nil || a != b {
		t.Fatalf("equivalent visibility digest changed: %s %s %v", a, b, err)
	}
	changed := &managedCalendarSource{resolver: managedCalendarResolverFixture{scope: access.Scope{UserID: 42, ProfileID: "profile", ProfileVerified: true, AllowedLibraryIDs: []int{1}, DisabledLibraryIDs: []int{3}}}}
	c, err := changed.Authority(t.Context(), p)
	if err != nil || a == c {
		t.Fatalf("visibility change not detected: %s %s %v", a, c, err)
	}
}

func TestManagedCalendarArtworkNeverExportsRemoteURLs(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://images.example/poster.jpg", ""},
		{"http://images.example/poster.jpg", ""},
		{"//images.example/poster.jpg", ""},
		{"s3://bucket/poster.jpg", ""},
		{"artwork/poster.jpg", "artwork/poster.jpg"},
		{"native-cover:entry", "native-cover:entry"},
		{"", ""},
	} {
		if got := managedCalendarArtworkID(tc.input); got != tc.want {
			t.Errorf("artwork %q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}
