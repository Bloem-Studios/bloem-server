package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/music"
)

type musicFilterRecorder struct {
	musicRepoStub
	got *catalog.AccessFilter
}

func (s musicFilterRecorder) ListArtists(_ context.Context, _ int, _ string, _ int, f catalog.AccessFilter) (music.ArtistPage, error) {
	*s.got = f
	return music.ArtistPage{}, nil
}

// The native music surface must carry the viewer's rating ceiling (and the
// rest of the viewer scope) into the repository, not only the library lists:
// a restricted profile otherwise browses albums above its rating limit.
func TestNativeMusicHandlerForwardsViewerRatingCeiling(t *testing.T) {
	var got catalog.AccessFilter
	h := NewNativeMusicHandler(musicFilterRecorder{got: &got})
	req := httptest.NewRequest(http.MethodGet, "/api/bloem/v1/music/artists?library_id=7", nil)
	req = req.WithContext(access.SetScope(req.Context(), access.Scope{
		UserID: 1, AllowedLibraryIDs: []int{7}, DisabledLibraryIDs: nil, MaxContentRating: "PG", MaxPlaybackQuality: "1080p",
	}))
	rec := httptest.NewRecorder()
	h.HandleArtists(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got.MaxContentRating != "PG" {
		t.Fatalf("MaxContentRating = %q, want PG", got.MaxContentRating)
	}
	if len(got.AllowedLibraryIDs) != 1 || got.AllowedLibraryIDs[0] != 7 {
		t.Fatalf("AllowedLibraryIDs = %v", got.AllowedLibraryIDs)
	}
}
