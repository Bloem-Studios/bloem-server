package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type playbackOriginRecorder struct {
	userstore.UserStore
	calls chan bool
}

func (s *playbackOriginRecorder) UpdateProgress(ctx context.Context, _, _ string, _, _ float64, _ userstore.ProgressThresholds) error {
	s.calls <- userstore.IsLegacyPlaybackWrite(ctx)
	return userstore.ErrPlaybackSourceUnbound
}
func (s *playbackOriginRecorder) SetProgress(ctx context.Context, _, _ string, _, _ float64, _ userstore.ProgressThresholds) error {
	s.calls <- userstore.IsLegacyPlaybackWrite(ctx)
	return userstore.ErrPlaybackSourceUnbound
}
func (s *playbackOriginRecorder) UpdateProgressHints(ctx context.Context, _, _ string, _ userstore.VersionHints) error {
	s.calls <- userstore.IsLegacyPlaybackWrite(ctx)
	return userstore.ErrPlaybackSourceUnbound
}

func TestNativePlaybackOriginsIncludeDelayedExpiry(t *testing.T) {
	for _, mode := range []string{"progress", "stop", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			store := &playbackOriginRecorder{UserStore: newPlaybackTestStore(t), calls: make(chan bool, 2)}
			file := &models.MediaFile{ID: 42, ContentID: "movie-1", Duration: 3600}
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0), testPlaybackFileResolver{file: file})
			handler.StoreProvider = testUserStoreProvider{store: store}
			session := &playback.Session{ID: "old-session", UserID: 1, ProfileID: "profile-1", MediaFileID: 42, Position: 240}
			want := 1
			switch mode {
			case "progress":
				handler.persistProgress(t.Context(), session)
				want = 2
			case "stop":
				handler.persistStopAndHistory(t.Context(), session)
			case "expiry":
				handler.handleExpiredSession(session)
			}
			for range want {
				select {
				case marked := <-store.calls:
					if !marked {
						t.Fatal("legacy callback lost playback origin")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("callback did not reach persistence")
				}
			}
		})
	}
}
