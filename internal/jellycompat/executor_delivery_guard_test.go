package jellycompat

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestCompatChildRouteRejectsExecutorBinding(t *testing.T) {
	source := PlaybackMediaSource{}
	assignment := &playback.NodeRoutingAssignment{Workload: "video_transcode", Execution: "api", Egress: "api"}
	card := &playback.RecipeCard{}
	if !compatChildHLSRouteMatches(source, card, assignment) {
		t.Fatal("legacy route fixture must remain valid")
	}
	card.Executor = new(playback.ExecutorNamespaceV3)
	w := httptest.NewRecorder()
	if requireCompatLegacyExecutor(w, &PlaybackSession{Recipe: card}) || w.Code != http.StatusServiceUnavailable {
		t.Fatal("bound direct/master accepted")
	}
	if compatChildHLSRouteMatches(source, card, assignment) {
		t.Fatal("bound recipe entered unguarded compatibility response")
	}
}
