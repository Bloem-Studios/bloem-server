package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestNativeOriginRejectsExecutorBoundDelivery(t *testing.T) {
	// Even an incomplete binding must not downgrade to the legacy response path.
	executor := new(playback.ExecutorNamespaceV3)
	for name, check := range map[string]func(http.ResponseWriter) bool{
		"live metadata": func(w http.ResponseWriter) bool {
			return requireNativeSessionAPIEgressV3(w, &playback.Session{Executor: executor})
		},
		"cold recipe": func(w http.ResponseWriter) bool {
			return requireNativeRecipeAPIEgressV3(w, &playback.RecipeCard{Executor: executor})
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			if check(w) || w.Code != http.StatusServiceUnavailable {
				t.Fatalf("bound origin accepted: status %d", w.Code)
			}
		})
	}
	if !requireNativeSessionAPIEgressV3(httptest.NewRecorder(), &playback.Session{}) {
		t.Fatal("legacy session refused")
	}
	if !requireNativeRecipeAPIEgressV3(httptest.NewRecorder(), &playback.RecipeCard{}) {
		t.Fatal("legacy recipe refused")
	}
}
