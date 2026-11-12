package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
)

type fixedCompatScope struct {
	scope access.Scope
	err   error
	calls int
}

func (f *fixedCompatScope) Resolve(context.Context, access.ResolveInput) (access.Scope, error) {
	f.calls++
	scope := f.scope
	scope.AllowedLibraryIDs = append([]int(nil), f.scope.AllowedLibraryIDs...)
	if f.scope.AllowedLibraryIDs == nil {
		scope.AllowedLibraryIDs = nil
	}
	return scope, f.err
}

type fixedCompatLibraries struct {
	without, with []int
	err           error
	calls         int
}

func (f *fixedCompatLibraries) LibrariesByStorage(context.Context) ([]int, []int, error) {
	f.calls++
	return f.without, f.with, f.err
}

// Phase 0 of Jellyfin native playback: until Jellyfin clients can play
// native-storage files, the compatibility server leaves every library that
// reads from a storage source out of the viewer's scope. Browse, search,
// latest, next up and item reads all filter by that scope.
func TestCompatScopeHidesStorageBackedLibraries(t *testing.T) {
	libraries := &fixedCompatLibraries{without: []int{1, 2}, with: []int{3, 4}}
	cases := []struct {
		name    string
		allowed []int
		want    []int
	}{
		{name: "restricted scope loses storage libraries", allowed: []int{1, 3, 4}, want: []int{1}},
		{name: "unrestricted scope becomes the libraries without storage", allowed: nil, want: []int{1, 2}},
		{name: "only storage libraries leaves an empty allowlist", allowed: []int{3}, want: []int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := &fixedCompatScope{scope: access.Scope{UserID: 7, ProfileID: "p", AllowedLibraryIDs: tc.allowed, LibrariesRestricted: tc.allowed != nil, PlaybackAllowed: true}}
			scope, err := hideStorageLibraries(next, libraries).Resolve(t.Context(), access.ResolveInput{UserID: 7, ProfileID: "p"})
			if err != nil {
				t.Fatal(err)
			}
			if scope.AllowedLibraryIDs == nil || !reflect.DeepEqual(scope.AllowedLibraryIDs, tc.want) {
				t.Fatalf("AllowedLibraryIDs = %#v, want %#v", scope.AllowedLibraryIDs, tc.want)
			}
			if !scope.LibrariesRestricted || scope.UserID != 7 || scope.ProfileID != "p" || !scope.PlaybackAllowed {
				t.Fatalf("scope changed beyond its libraries: %+v", scope)
			}
		})
	}
}

func TestCompatScopeWithoutStorageLibrariesIsUnchanged(t *testing.T) {
	next := &fixedCompatScope{scope: access.Scope{UserID: 7, AllowedLibraryIDs: nil}}
	scope, err := hideStorageLibraries(next, &fixedCompatLibraries{without: []int{1}}).Resolve(t.Context(), access.ResolveInput{UserID: 7})
	if err != nil || scope.AllowedLibraryIDs != nil || scope.LibrariesRestricted {
		t.Fatalf("scope = %+v, err = %v; want the unrestricted scope", scope, err)
	}
}

func TestCompatScopeFailsClosedWhenStorageLibrariesAreUnknown(t *testing.T) {
	next := &fixedCompatScope{scope: access.Scope{UserID: 7, AllowedLibraryIDs: nil}}
	if _, err := hideStorageLibraries(next, &fixedCompatLibraries{err: errors.New("database down")}).Resolve(t.Context(), access.ResolveInput{UserID: 7}); err == nil {
		t.Fatal("scope resolved without knowing which libraries read from storage")
	}
	failing := &fixedCompatScope{err: errors.New("viewer revoked")}
	if _, err := hideStorageLibraries(failing, &fixedCompatLibraries{}).Resolve(t.Context(), access.ResolveInput{UserID: 7}); err == nil {
		t.Fatal("viewer resolution error was swallowed")
	}
}

func TestCompatStorageLibrariesAreCachedBriefly(t *testing.T) {
	libraries := &fixedCompatLibraries{without: []int{1}, with: []int{2}}
	now := time.Unix(1000, 0)
	hider := hideStorageLibraries(&fixedCompatScope{scope: access.Scope{AllowedLibraryIDs: []int{1, 2}}}, libraries).(*bloemCompatNativeLibraryHider)
	hider.now = func() time.Time { return now }
	for range 3 {
		if _, err := hider.Resolve(t.Context(), access.ResolveInput{}); err != nil {
			t.Fatal(err)
		}
	}
	if libraries.calls != 1 {
		t.Fatalf("library lookups = %d within the cache window, want 1", libraries.calls)
	}
	now = now.Add(bloemCompatStorageLibrariesTTL + time.Millisecond)
	if _, err := hider.Resolve(t.Context(), access.ResolveInput{}); err != nil {
		t.Fatal(err)
	}
	if libraries.calls != 2 {
		t.Fatalf("library lookups = %d after the cache window, want 2", libraries.calls)
	}
}

// The compatibility server's scope resolver, which both its catalog filter
// and its playback checks use, must hide storage-backed libraries.
func TestCompatScopeResolverHidesStorageLibrariesInMain(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	hide := strings.Index(text, "compatScopeResolver = bloemCompatHideNativeLibraries(compatScopeResolver, ")
	filter := strings.Index(text, "compatDeps.AccessFilterFn = jellycompat.NewScopeAccessFilter(compatScopeResolver)")
	playback := strings.Index(text, "compatDeps.PlaybackScopeResolver = compatScopeResolver")
	if hide < 0 || filter < 0 || playback < 0 || hide > filter || hide > playback {
		t.Fatalf("main.go must wrap compatScopeResolver with bloemCompatHideNativeLibraries before the compatibility server uses it (hide=%d filter=%d playback=%d)", hide, filter, playback)
	}
}
