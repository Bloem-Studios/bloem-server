package jellycompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// TestHandleDownload_ServesOriginalFile verifies /Items/{id}/Download streams
// the original media file. The route backs CanDownload=true, which Infuse
// requires before it will Direct Play an item.
func TestHandleDownload_ServesOriginalFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "movie.mkv")
	content := []byte("fake media bytes")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	codec := NewResourceIDCodec()
	contentID := "movie-1"
	detail := &upstreamItemDetail{
		ContentID: contentID,
		Type:      "movie",
		Versions: []catalog.FileVersion{{
			FileID:    42,
			FilePath:  filePath,
			Container: "mkv",
			Duration:  3600,
			AddedAt:   time.Now(),
		}},
	}
	handler := &PlaybackHandler{
		codec:        codec,
		content:      &stubContentService{detail: detail},
		fileResolver: testCompatFileResolver{file: &models.MediaFile{ID: 42, FilePath: filePath}},
	}

	encodedID := codec.EncodeStringID(EncodedIDItem, contentID)
	req := httptest.NewRequest("GET", "/Items/"+encodedID+"/Download", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", encodedID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	ctx = context.WithValue(ctx, compatSessionKey, &Session{StreamAppUserID: 1, ProfileID: "profile-1"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.HandleDownload(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected status 200; got %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(content) {
		t.Errorf("expected file content %q; got %q", content, got)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd == "" {
		t.Error("expected Content-Disposition header on download response")
	}
}

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

// TestItemDetail_AdvertisesCanDownload guards against regressing the
// CanDownload flag: Infuse refuses Direct Play (Static=true streaming) of
// items it believes it cannot download, so playable items must advertise it.
func TestItemDetail_AdvertisesCanDownload(t *testing.T) {
	m := newMapper(NewResourceIDCodec(), nil)
	detail := upstreamItemDetail{
		ContentID: "movie-1",
		Type:      "movie",
		Versions: []catalog.FileVersion{{
			FileID:    42,
			Container: "mkv",
			Duration:  3600,
			AddedAt:   time.Now(),
		}},
	}
	dto := m.itemFromDetailWithFields(detail, false, nil, nil)
	if !dto.CanDownload {
		t.Error("playable item detail must advertise CanDownload=true; Infuse requires it for Direct Play")
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
