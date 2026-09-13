package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestEnsureUpstreamPlaybackRechecksBrowseOnlyForExistingSession(t *testing.T) {
	mgr := &testCompatSessionManager{}
	h, store := newActiveEncodingsHandler(mgr)
	h.accessFilter = func(context.Context, int, string) catalog.AccessFilter {
		return catalog.AccessFilter{PlaybackDenied: true}
	}
	source := PlaybackMediaSource{ID: "source-1", FileID: 42, Version: testCompatVersion()}
	store.Put(PlaybackSession{ID: "play-1", CompatToken: "token-1", ItemID: "movie-1", UpstreamSessionID: "already-live", UpstreamPlayMethod: "direct", MediaSources: []PlaybackMediaSource{source}})
	_, err := h.ensureUpstreamPlayback(context.Background(), &Session{Token: "token-1", StreamAppUserID: 7, ProfileID: "browse-only"}, "play-1", source, "direct")
	if !errors.Is(err, playback.ErrPlaybackNotAllowed) {
		t.Fatalf("ensureUpstreamPlayback() error = %v, want ErrPlaybackNotAllowed", err)
	}
}
