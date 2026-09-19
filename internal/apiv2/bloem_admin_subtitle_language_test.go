package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

type bloemAdminSubtitleLanguageService struct {
	page  handlers.AdminSubtitlePage
	calls int
}

func (f *bloemAdminSubtitleLanguageService) ListAdminSubtitlesPage(context.Context, handlers.AdminSubtitleListFilter, *handlers.AdminSubtitlePageKey, int) (handlers.AdminSubtitlePage, error) {
	f.calls++
	// Share the backing slice so an in-place projection would change the source.
	return f.page, nil
}

func TestBloemAdminStoredSubtitleCanonicalizesLegacyLanguageWithoutMutation(t *testing.T) {
	fake := &bloemAdminSubtitleLanguageService{page: handlers.AdminSubtitlePage{
		Items: []handlers.AdminDownloadedSubtitle{{
			ID: 91, MediaFileID: 42, Provider: "subdl", Language: " English ",
			Format: "srt", ReleaseName: "Legacy subtitle", CreatedAt: fixedTime(),
		}},
		Total: 1, ProviderDownloads: 1,
	}}
	before := slices.Clone(fake.page.Items)
	deps := requestDeps(fixtureRequests())
	deps.AdminSubtitleList = fake
	h := newTestHandler(t, deps)

	reply := do(t, h, http.MethodGet, Prefix+"/admin/subtitles", "", actingRequestAdmin)
	if reply.Code != http.StatusOK {
		t.Fatalf("subtitle list status = %d, want %d", reply.Code, http.StatusOK)
	}
	var body AdminStoredSubtitleCollection
	if err := json.Unmarshal(reply.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode subtitle list: %v", err)
	}
	if fake.calls != 1 || len(body.Items) != 1 {
		t.Fatalf("store calls = %d, items = %d; want one each", fake.calls, len(body.Items))
	}
	row := body.Items[0]
	if row.ID != "91" || row.MediaFileID != "42" || row.Provider != "subdl" || row.Language != "en" {
		t.Fatal("v2 did not preserve subtitle identity and canonicalize the legacy language")
	}
	if body.Total != 1 || body.Uploads != 0 || body.ProviderDownloads != 1 || body.Page == nil || body.Page.HasMore || body.Page.NextCursor != "" {
		t.Fatal("language projection changed filtered counts or terminal page state")
	}
	if !slices.Equal(fake.page.Items, before) {
		t.Fatal("language projection changed the source subtitle rows")
	}
}

func TestBloemAdminStoredSubtitleRejectsInvalidLanguageWithoutPartialPage(t *testing.T) {
	fake := &bloemAdminSubtitleLanguageService{page: handlers.AdminSubtitlePage{
		Items: []handlers.AdminDownloadedSubtitle{
			{ID: 92, MediaFileID: 42, Provider: "subdl", Language: " English ", CreatedAt: fixedTime()},
			{ID: 91, MediaFileID: 42, Provider: "subdl", Language: "not a language!", CreatedAt: fixedTime()},
		},
		Total: 3, ProviderDownloads: 3, HasMore: true,
	}}
	before := slices.Clone(fake.page.Items)
	deps := requestDeps(fixtureRequests())
	deps.AdminSubtitleList = fake
	h := newTestHandler(t, deps)

	// The invalid row follows a valid one, so a partial projection is observable.
	reply := do(t, h, http.MethodGet, Prefix+"/admin/subtitles?limit=2", "", actingRequestAdmin)
	requireProblem(t, reply, TypeInternalError)
	if fake.calls != 1 {
		t.Fatalf("store calls = %d, want 1", fake.calls)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(reply.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode subtitle problem: %v", err)
	}
	for _, field := range []string{"items", "page", "total", "uploads", "provider_downloads"} {
		if _, present := body[field]; present {
			t.Errorf("invalid-language problem contains partial collection field %q", field)
		}
	}
	if !slices.Equal(fake.page.Items, before) {
		t.Fatal("failed language projection changed the source subtitle rows")
	}
}
