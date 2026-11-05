package apiv2

import (
	"context"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// bloemPromotionRecordingHome records the promoted opt-in each home read
// receives on its context, where the section transformers read it.
type bloemPromotionRecordingHome struct {
	*fakeHome
	promoted []string
}

func (f *bloemPromotionRecordingHome) record(ctx context.Context) {
	f.promoted = append(f.promoted, handlers.SectionOptions(ctx).Get("promoted"))
}

func (f *bloemPromotionRecordingHome) HomeLayoutWithItems(ctx context.Context, viewer handlers.SectionViewer, includeItems int) (handlers.HomeLayoutView, error) {
	f.record(ctx)
	return f.fakeHome.HomeLayoutWithItems(ctx, viewer, includeItems)
}

func (f *bloemPromotionRecordingHome) HomeSections(ctx context.Context, viewer handlers.SectionViewer) (handlers.SectionsView, error) {
	f.record(ctx)
	return f.fakeHome.HomeSections(ctx, viewer)
}

func (f *bloemPromotionRecordingHome) HomeSectionItems(ctx context.Context, sectionID string, viewer handlers.SectionViewer) (handlers.SectionView, error) {
	f.record(ctx)
	return f.fakeHome.HomeSectionItems(ctx, sectionID, viewer)
}

func TestBloemHomeLayoutBundlingKeepsPromotionOptIn(t *testing.T) {
	deps, fake := homeDeps(t)
	h := newTestHandler(t, deps)
	response := do(t, h, http.MethodGet, "/api/v2/home/layout?promoted=1&include_items=1&image_size=small", "", viewerHeaders())
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	if fake.lastInclude != 1 || fake.lastViewer.ImageSize != "small" {
		t.Fatalf("include=%d viewer=%+v", fake.lastInclude, fake.lastViewer)
	}
	// Other home reads retain only their own query parameters.
	requireProblem(t, do(t, h, http.MethodGet, "/api/v2/home/sections?include_items=1", "", viewerHeaders()), TypeValidationFailed)
}

// TestBloemHomeReadsCarryPromotionOptIn pins that each v2 home read hands
// the promoted=1 opt-in, and only that, to the section transformers.
func TestBloemHomeReadsCarryPromotionOptIn(t *testing.T) {
	const home = "/api/v2/home"
	for _, path := range []string{home + "/layout", home + "/sections", home + "/sections/continue_watching/items"} {
		for query, want := range map[string]string{"?promoted=1": "1", "": ""} {
			deps, fake := homeDeps(t)
			recording := &bloemPromotionRecordingHome{fakeHome: fake}
			deps.HomeSections = recording
			response := do(t, newTestHandler(t, deps), http.MethodGet, path+query, "", viewerHeaders())
			if response.Code != http.StatusOK {
				t.Fatal(path+query, response.Code, response.Body.String())
			}
			if len(recording.promoted) != 1 || recording.promoted[0] != want {
				t.Fatalf("%s%s: promoted opt-in on the context = %q, want %q", path, query, recording.promoted, want)
			}
		}
	}
}
