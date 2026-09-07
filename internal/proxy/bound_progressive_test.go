package proxy

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func TestBoundProgressiveProxyRequiresSelectedEgressAndServeGrant(t *testing.T) {
	ns := playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), ExecutorID: uuid.NewString(), Epoch: 1}
	card := playback.RecipeCard{Executor: &ns, SessionID: "s", TranscodeTransportID: "t", UserID: 1, ProfileID: "p", MediaFileID: 1, PlayMethod: playback.PlayRemux, InputPath: "synthetic", TotalDuration: 2, RemuxDVMode: playback.RemuxDVPreserveV3, RoutingWorkload: "remux", RoutingExecution: "transcode", RoutingExecutionNodeID: 1, TranscodeNodeURL: "http://invalid.example", RoutingEgress: "proxy", RoutingEgressNodeID: 2}
	calls := 0
	acquire := func(_ context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		calls++
		if transport != "t" || executor != ns || purpose != playback.AttemptGrantServeV3 {
			t.Fatal("wrong final authority")
		}
		return nil, errors.New("denied")
	}
	transfer := func(context.Context, string, playback.ExecutorNamespaceV3) (string, func(), error) {
		t.Fatal("transfer opened before final grant")
		return "", nil, nil
	}
	if err := ServeBoundProgressiveV3(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), card, 3, "", acquire, transfer); err == nil || calls != 0 {
		t.Fatal("foreign egress acquired grant")
	}
	if err := ServeBoundProgressiveV3(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), card, 2, "", acquire, transfer); err == nil || calls != 1 {
		t.Fatal("missing grant served")
	}
}
