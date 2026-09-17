package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestBloemBridgeStopOnAnotherReplica(t *testing.T) {
	f := newPlaybackServiceFixture(t)
	remote := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	remote.PlanStoreV3 = f.handler.PlanStoreV3
	for _, user := range []int{2, 1, 1} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/playback/"+f.session.ID, nil)
		ctx := apimw.SetClaims(f.ctx, &auth.Claims{UserID: user, TokenType: auth.TokenTypeAccess})
		rec := httptest.NewRecorder()
		if !remote.stopDurablePlayback(rec, req.WithContext(ctx), f.session.ID) {
			t.Fatal("shared attempt not handled")
		}
		want := http.StatusNoContent
		if user == 2 {
			want = http.StatusForbidden
		}
		if rec.Code != want {
			t.Fatalf("stop user %d: %d %s", user, rec.Code, rec.Body.String())
		}
		record, err := remote.PlanStoreV3.GetAttempt(ctx, f.session.ID)
		if err != nil || (record.StoppedAt != nil) != (user == 1) {
			t.Fatalf("stop authority incorrect: %+v %v", record, err)
		}
	}
	if _, err := remote.PlanStoreV3.(playback.ProgressStoreV3).ApplyProgress(f.ctx, f.session.ID, playback.ProgressSampleV3{Sequence: 1, Position: 2}); !errors.Is(err, playback.ErrAttemptStoppedV3) {
		t.Fatalf("late progress: %v", err)
	}
}
