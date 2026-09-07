package handlers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestBoundProgressiveAdapterRefusesMissingProducerAndForeignEgress(t *testing.T) {
	adapter := &BoundProgressiveAdapterV3{}
	card := playback.RecipeCard{RoutingExecution: "api"}
	if err := adapter.Start(t.Context(), card, nil); err == nil {
		t.Fatal("unprepared local start")
	}
	if err := adapter.Serve(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), card); err == nil {
		t.Fatal("missing runtime served")
	}
	card.RoutingExecution = "transcode"
	// Remote cleanup is exclusively durable cancellation/grant revocation; no
	// node URL can cause an invented legacy DELETE or replacement request.
	if err := adapter.Stop(context.Background(), card); err == nil {
		t.Fatal("remote cleanup bypassed authority")
	}
}
