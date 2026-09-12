package handlers

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
)

func scopedCtx(allowed []int, disabled []int) context.Context {
	return access.SetScope(context.Background(), access.Scope{
		UserID:              7,
		AllowedLibraryIDs:   allowed,
		DisabledLibraryIDs:  disabled,
		LibrariesRestricted: true,
	})
}

func TestAllowsFileInsideScope(t *testing.T) {
	file := &models.MediaFile{ID: 1, MediaFolderID: 3}
	if !playbackLibraryAllowsFileCtx(scopedCtx([]int{3}, nil), file) {
		t.Fatal("a file in an allowed library must be served")
	}
}

func TestRefusesFileOutsideScope(t *testing.T) {
	file := &models.MediaFile{ID: 1, MediaFolderID: 4}
	if playbackLibraryAllowsFileCtx(scopedCtx([]int{3}, nil), file) {
		t.Fatal("a file outside the allowed libraries must not be served")
	}
}

func TestRefusesFileInDisabledLibrary(t *testing.T) {
	file := &models.MediaFile{ID: 1, MediaFolderID: 3}
	if playbackLibraryAllowsFileCtx(scopedCtx([]int{3}, []int{3}), file) {
		t.Fatal("a disabled library must hide its files")
	}
}

func TestUnrestrictedScopeAllowsWithoutResolvingTheFile(t *testing.T) {
	// A nil allow-list with no disabled libraries means the policy imposed no
	// library limit; the guard must not need a resolver to serve.
	if !playbackLibraryAllowsSourceCtx(scopedCtx(nil, nil), nil, 42) {
		t.Fatal("an unrestricted scope must serve without a resolver")
	}
}

func TestRestrictedScopeWithNoResolverFailsClosed(t *testing.T) {
	if playbackLibraryAllowsSourceCtx(scopedCtx([]int{3}, nil), nil, 42) {
		t.Fatal("a restricted scope with no resolver must fail closed")
	}
}

// TestNoScopeInContextFailsClosed covers the case a nil AllowedLibraryIDs
// masked: a request that never resolved a viewer scope at all (e.g. a
// stream-token request whose gate skipped scope resolution) produces the
// exact same zero-value AccessFilter as a genuinely unrestricted one once
// read through AccessFilterFromContext. The guard must tell those apart by
// consulting access.GetScope's own ok, not by treating an absent scope as an
// unrestricted policy.
func TestNoScopeInContextFailsClosed(t *testing.T) {
	ctx := context.Background()
	file := &models.MediaFile{ID: 1, MediaFolderID: 3}
	if playbackLibraryAllowsFileCtx(ctx, file) {
		t.Fatal("no resolved scope must fail closed, not allow")
	}
	if playbackLibraryAllowsSourceCtx(ctx, nil, 42) {
		t.Fatal("no resolved scope must fail closed even for an unrestricted-shaped filter")
	}
}
