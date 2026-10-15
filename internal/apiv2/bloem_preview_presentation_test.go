package apiv2

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type presentationSource struct{ allowed bool }

func (s *presentationSource) AccountPresentation(context.Context, *auth.Claims) (*preview.Presentation, bool, error) {
	return nil, s.allowed, nil
}
func TestPlaybackCapabilityRevalidationUsesEffectivePermission(t *testing.T) {
	deps := pilotDeps(nil, nil)
	deps.Playback = &fakePlaybackService{}
	source := &presentationSource{allowed: true}
	deps.PreviewPresentation = source
	h := NewHandler(deps)
	first := do(t, h, "GET", Prefix+"/playback/capabilities", "", profileOwner())
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	source.allowed = false
	denied := do(t, h, "GET", Prefix+"/playback/capabilities", "", with(profileOwner(), "If-None-Match", first.Header().Get("ETag")))
	if denied.Code != 200 {
		t.Fatalf("permission change returned %d: %s", denied.Code, denied.Body)
	}
	if first.Header().Get("ETag") == denied.Header().Get("ETag") {
		t.Fatal("effective permission not covered by capability validator")
	}
}
