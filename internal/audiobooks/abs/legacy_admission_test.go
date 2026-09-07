package abs

import (
	"context"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type refusedProgress struct{ fakeProgressStore }

func (*refusedProgress) UpsertProgress(context.Context, ProgressRow) error {
	return userstore.ErrPlaybackSourceUnbound
}
func (*refusedProgress) UpdateProgressPosition(context.Context, string, string, string, float64) error {
	return userstore.ErrPlaybackSourceUnbound
}

func TestABSRefusedStoreDoesNotAcknowledgeOrPublish(t *testing.T) {
	for _, kind := range []string{"local", "session"} {
		t.Run(kind, func(t *testing.T) {
			publisher := &recordingPublisher{}
			h := New(Dependencies{MediaStore: &stubMediaStore{known: map[string]*models.MediaItem{"book": nil}}, ProgressStore: &refusedProgress{}, Publisher: publisher, PlaybackSessionStore: &fakePlaybackSessionStore{sessions: map[string]ABSPlaybackSession{"session": {ID: "session", UserID: "1", ProfileID: "p", ContentID: "book"}}}})
			handler := h.handleSyncLocalSession
			body := []byte(`{"id":"queued","libraryItemId":"book","currentTime":80}`)
			if kind == "session" {
				handler = h.handleSessionSync
				body = []byte(`{"currentTime":80}`)
			}
			w := dispatchABSWithParams(http.MethodPost, "/fixture", map[string]string{"sid": "session"}, body, "1", "p", handler)
			if w.Code < 400 {
				t.Fatalf("refused write acknowledged: %d %s", w.Code, w.Body.String())
			}
			if len(publisher.snapshot()) != 0 {
				t.Fatal("refused write emitted success events")
			}
		})
	}
}
