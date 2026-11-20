package apiv2

import (
	"net/http"
	"sync"
	"testing"
)

// Routers built at the same time share Silo's read-only runtime schemas, so
// the preview projection must not write into them. Run with -race.
func TestPreviewProjectionLeavesSharedSchemasReadOnly(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	codes := make(chan int, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deps := pilotDeps(nil, nil)
			deps.Playback = &fakePlaybackService{}
			if i%2 == 0 {
				deps.PreviewPresentation = &presentationSource{allowed: true}
			}
			rec := do(t, NewHandler(deps), http.MethodGet, Prefix+"/playback/capabilities", "", profileOwner())
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("playback capabilities answered %d", code)
		}
	}
	schema := sharedRuntimeSchemas().schemas["Account"]
	if schema == nil || schema.Properties["preview"] == nil || schema.Properties["playback_allowed"] == nil {
		t.Fatal("the shared Account schema lost the preview properties")
	}
}
