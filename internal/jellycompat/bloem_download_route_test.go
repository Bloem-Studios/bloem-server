package jellycompat

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

func TestHandleDownload_BrowseOnlyPolicyDeniesDirectPlayTransport(t *testing.T) {
	handler := &PlaybackHandler{
		accessFilter: func(context.Context, int, string) catalog.AccessFilter {
			return catalog.AccessFilter{PlaybackDenied: true}
		},
	}
	req := httptest.NewRequest("GET", "/Items/ignored/Download", nil)
	ctx := context.WithValue(req.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "browse-only"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.HandleDownload(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleDownload_DownloadPolicyDeniesOriginalFileTransport(t *testing.T) {
	handler := &PlaybackHandler{accessFilter: func(context.Context, int, string) catalog.AccessFilter {
		return catalog.AccessFilter{DownloadDenied: true}
	}}
	req := httptest.NewRequest("GET", "/Items/ignored/Download", nil)
	req = req.WithContext(context.WithValue(req.Context(), compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "no-download"}))
	rec := httptest.NewRecorder()
	handler.HandleDownload(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestItemDetail_BrowseOnlyPolicyDoesNotAdvertiseCanDownload(t *testing.T) {
	h := &ItemsHandler{
		mapper: newMapper(NewResourceIDCodec(), nil),
		accessFilter: func(context.Context, int, string) catalog.AccessFilter {
			return catalog.AccessFilter{PlaybackDenied: true}
		},
	}
	detail := upstreamItemDetail{
		ContentID: "movie-1",
		Type:      "movie",
		Versions:  []catalog.FileVersion{{FileID: 42}},
	}
	dto := h.itemFromDetailForSession(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "browse-only"}, detail, false, nil, nil)
	if dto.CanDownload {
		t.Fatal("CanDownload = true, want browse-only policy not to advertise direct-play transport")
	}
}

func TestItemDetail_PlaybackAllowedDownloadDeniedDoesNotAdvertiseCanDownload(t *testing.T) {
	h := &ItemsHandler{mapper: newMapper(NewResourceIDCodec(), nil), accessFilter: func(context.Context, int, string) catalog.AccessFilter {
		return catalog.AccessFilter{DownloadDenied: true}
	}}
	detail := upstreamItemDetail{ContentID: "movie-1", Type: "movie", Versions: []catalog.FileVersion{{FileID: 42}}}
	dto := h.itemFromDetailForSession(context.Background(), &Session{StreamAppUserID: 1, ProfileID: "no-download"}, detail, false, nil, nil)
	if dto.CanDownload {
		t.Fatal("CanDownload = true, want download-disabled policy not to advertise Jelly download/direct-play transport")
	}
}
