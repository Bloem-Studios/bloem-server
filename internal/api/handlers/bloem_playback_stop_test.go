package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type bloemStopAttemptProbe struct {
	*playback.MemoryPlanStoreV3
	calls int
	err   error
}

func (s *bloemStopAttemptProbe) GetAttempt(context.Context, string) (*playback.AttemptRecordV3, error) {
	s.calls++
	return nil, s.err
}

func TestBloemBridgeStopInvalidIDIsNotAStoreOutage(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		err      error
		status   int
		calls    int
	}{
		{"malformed", "not-a-session-uuid", errors.New("store unavailable"), http.StatusNotFound, 0},
		{"absent", uuid.NewString(), playback.ErrSessionNotFound, http.StatusNotFound, 1},
		{"store outage", uuid.NewString(), errors.New("store unavailable"), http.StatusServiceUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &bloemStopAttemptProbe{MemoryPlanStoreV3: playback.NewMemoryPlanStoreV3(), err: tc.err}
			handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
			handler.PlanStoreV3 = store
			router := chi.NewRouter()
			router.Delete("/playback/{session_id}", handler.HandleStopPlayback)
			req := httptest.NewRequest(http.MethodDelete, "/playback/"+tc.id, nil)
			ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAccess})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req.WithContext(ctx))
			if recorder.Code != tc.status || store.calls != tc.calls {
				t.Fatalf("status/calls = %d/%d, want %d/%d; body=%s", recorder.Code, store.calls, tc.status, tc.calls, recorder.Body.String())
			}
		})
	}
}

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
