package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/ambience"
)

type publicAmbienceStub struct{ packs []ambience.Wire }

func (s publicAmbienceStub) ActivePublic(context.Context) ([]ambience.Wire, error) {
	return s.packs, nil
}

// The signed-out seasonal layer reads the deployment-wide packs from a native
// route, not from Silo's branding.
func TestPublicAmbienceListsTheDeploymentWidePacks(t *testing.T) {
	h := NewBloemPublicAmbienceHandler(publicAmbienceStub{packs: []ambience.Wire{{ID: "winter"}}})
	rec := httptest.NewRecorder()
	h.HandleGet(rec, httptest.NewRequest(http.MethodGet, "/ambience/public", nil))
	var body BloemPublicAmbienceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || len(body.Ambience) != 1 || body.Ambience[0].ID != "winter" {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestPublicAmbienceWithoutARegistryIsEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	NewBloemPublicAmbienceHandler(nil).HandleGet(rec, httptest.NewRequest(http.MethodGet, "/ambience/public", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"ambience\":[]}\n" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
}
