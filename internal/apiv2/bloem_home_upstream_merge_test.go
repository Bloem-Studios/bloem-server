package apiv2

import (
	"net/http"
	"testing"
)

func TestBloemHomeLayoutBundlingKeepsPromotionOptIn(t *testing.T) {
	deps, fake := homeDeps(t)
	h := newTestHandler(t, deps)
	response := do(t, h, http.MethodGet, "/api/v2/home/layout?promoted=1&include_items=1&image_size=small", "", viewerHeaders())
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	if fake.lastInclude != 1 || fake.lastViewer.ImageSize != "small" || fake.lastViewer.Extensions.Get("promoted") != "1" {
		t.Fatalf("include=%d viewer=%+v", fake.lastInclude, fake.lastViewer)
	}
	// Other home reads retain only their own query parameters.
	requireProblem(t, do(t, h, http.MethodGet, "/api/v2/home/sections?include_items=1", "", viewerHeaders()), TypeValidationFailed)
}
