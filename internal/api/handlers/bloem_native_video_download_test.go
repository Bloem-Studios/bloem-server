package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func nativeDownloadFixture(t *testing.T, body string) (*NativeVideoDownloads, *models.MediaFile, *atomic.Bool) {
	t.Helper()
	file := &models.MediaFile{ID: 42, ContentID: "movie", MediaFolderID: 7, FilePath: "bloem-storage:movie", Container: "mkv", VideoTracks: []models.VideoTrack{{Codec: "h264"}}, FileSize: 3}
	identity := nativestorage.PlaybackIdentity{FileID: 42, MediaFolderID: 7, ContentID: "movie", FilePath: file.FilePath, Reference: storagesource.PersistedRef{Revision: "version-1"}}
	revoked := new(atomic.Bool)
	closed := new(int)
	d := &NativeVideoDownloads{Service: &NativeVideoService{}, Open: func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		inner := &videoFileFixture{Reader: bytes.NewReader([]byte(body)), id: identity, info: mediasource.Info{Name: "movie.mkv", Size: int64(len(body)), Revision: "version-1"}, closed: closed}
		return &nativeDeliverySource{NativeVideoFile: inner, revoked: revoked, reads: new(atomic.Int32), closes: new(atomic.Int32)}, nil
	}}
	return d, file, revoked
}

func TestNativeVideoDownloadServesRangesFromTheNativeReader(t *testing.T) {
	d, file, _ := nativeDownloadFixture(t, "0123456789")
	view, ok := d.DownloadFile(t.Context(), file)
	if !ok || view == file || view.FileSize != 3 {
		t.Fatalf("view=%+v ok=%v", view, ok)
	}
	d.Size = func(context.Context, *models.MediaFile) (int64, bool, error) { return 10, true, nil }
	if view, ok = d.DownloadFile(t.Context(), file); !ok || view.FileSize != 10 || file.FileSize != 3 {
		t.Fatalf("sized view=%+v ok=%v", view, ok)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=2-5")
	opened, err := d.OpenOriginal(t.Context(), req, file)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.ETag() != nativeVideoEntityTag(NativeVideoBinding{Identity: nativestorage.PlaybackIdentity{FileID: 42}, Info: mediasource.Info{Size: 10, Revision: "version-1"}}) || opened.Extension() != ".mkv" || opened.ContentType() != "video/x-matroska" {
		t.Fatalf("etag=%s ext=%s type=%s", opened.ETag(), opened.Extension(), opened.ContentType())
	}
	rr := httptest.NewRecorder()
	http.ServeContent(rr, req, "movie.mkv", opened.ModTime(), opened)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "2345" || opened.Err() != nil {
		t.Fatalf("status=%d body=%q err=%v", rr.Code, rr.Body.String(), opened.Err())
	}
}

func TestNativeVideoDownloadStopsWhenAuthorityIsWithdrawn(t *testing.T) {
	d, file, revoked := nativeDownloadFixture(t, "0123456789")
	revoked.Store(true)
	if _, err := d.OpenOriginal(t.Context(), httptest.NewRequest(http.MethodGet, "/", nil), file); err == nil {
		t.Fatal("opened without authority")
	}
	revoked.Store(false)
	opened, err := d.OpenOriginal(t.Context(), httptest.NewRequest(http.MethodGet, "/", nil), file)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	revoked.Store(true)
	// Reads past the one-second authorization window stop at a fresh check.
	reader := opened.(*nativeVideoDownload)
	if err := reader.Reauthorize(t.Context()); err == nil {
		t.Fatal("revoked source still authorized")
	}
	if _, err := io.ReadAll(opened); err == nil {
		t.Fatal("bytes left after a failed authority check")
	}
	if opened.Err() == nil {
		t.Fatal("failure not reported")
	}
}

func TestNativeVideoDownloadAdmitsOnlyNativeVideo(t *testing.T) {
	d, file, _ := nativeDownloadFixture(t, "x")
	audio := &models.MediaFile{ID: 1, FilePath: "bloem-storage:track", BaseType: "audiobook", CodecAudio: "mp3", Container: "mp3"}
	local := *file
	local.FilePath = "/fixture/movie.mkv"
	unknown := *file
	unknown.Container = "iso"
	for _, f := range []*models.MediaFile{audio, &local, &unknown, nil} {
		if _, ok := d.DownloadFile(t.Context(), f); ok {
			t.Fatalf("admitted %+v", f)
		}
	}
	d.Size = func(context.Context, *models.MediaFile) (int64, bool, error) {
		return 0, false, errors.New("store down")
	}
	if _, ok := d.DownloadFile(t.Context(), file); ok {
		t.Fatal("admitted with an unknown size")
	}
	if _, err := (*NativeVideoDownloads)(nil).OpenOriginal(t.Context(), nil, file); !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("nil err=%v", err)
	}
}

func TestNativeVideoDownloadErrorsMapToDownloadRefusals(t *testing.T) {
	if err := nativeVideoDownloadError(middleware.ErrCurrentCredentialForbidden); !errors.Is(err, downloads.ErrDownloadNotAllowed) {
		t.Fatalf("forbidden=%v", err)
	}
	if err := nativeVideoDownloadError(storagesource.ErrReferenceConflict); !errors.Is(err, catalog.ErrItemNotFound) {
		t.Fatalf("conflict=%v", err)
	}
}
