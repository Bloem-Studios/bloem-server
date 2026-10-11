package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type failedNativeVideoHistory struct{ NativeVideoBindings }

func (failedNativeVideoHistory) HasSession(context.Context, string) (bool, error) {
	return false, errors.New("native history unavailable")
}

func TestNativeVideoRemotePredecessorAuthorityAdmission(t *testing.T) {
	for _, scenario := range []string{"before_bind", "retained_after_catalog_change", "ordinary", "history_unavailable", "catalog_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			service, file, session, _, _ := nativeVideoFixture(t)
			service.Bindings = NewNativeVideoBindingStore(nil)
			wantRefusal, wantFreshStart := true, true
			switch scenario {
			case "retained_after_catalog_change":
				if err := service.Bind(t.Context(), session, file); err != nil {
					t.Fatal(err)
				}
				file.FilePath = "/fixture/replaced.mp4"
			case "ordinary":
				file.FilePath = "/fixture/ordinary.mp4"
				wantRefusal, wantFreshStart = false, false
			case "history_unavailable":
				file.FilePath = "/fixture/ordinary.mp4"
				service.Bindings = failedNativeVideoHistory{service.Bindings}
				wantFreshStart = false
			case "catalog_unavailable":
				service.Files = errStreamFileResolver{err: errors.New("catalog unavailable")}
				wantFreshStart = false
			}
			manager := service.Sessions.(*playback.SessionManager)
			// Keep the caller snapshot stale so admission has to inspect the
			// currently published worker route as well as retained authority.
			if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{NodeURL: "http://worker.example", TransportID: "previous"}); err != nil {
				t.Fatal(err)
			}
			h := NewPlaybackHandler(manager, service.Files)
			h.NativeVideo = service
			result := remoteHLSResultV3()
			result.Plan.SessionID = session.ID
			failure := h.nativeVideoPredecessorAdmissionV3(t.Context(), session, result)
			if !wantRefusal {
				if failure != nil {
					t.Fatalf("ordinary predecessor refused: %v", failure)
				}
				return
			}
			if failure == nil || failure.reason != "route_capability_unavailable" || !failure.retryable {
				t.Fatalf("uncertain/native predecessor admitted: %+v", failure)
			}
			if strings.Contains(failure.message, "fresh playback start") != wantFreshStart {
				t.Fatalf("wrong authority refusal: %+v", failure)
			}
		})
	}
}
