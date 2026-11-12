package downloads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type nativeOriginalFixture struct {
	*bytes.Reader
	closed  bool
	readErr error
}

func (f *nativeOriginalFixture) Close() error        { f.closed = true; return nil }
func (f *nativeOriginalFixture) ModTime() time.Time  { return time.Unix(1700000000, 0) }
func (f *nativeOriginalFixture) ETag() string        { return `"native-rev"` }
func (f *nativeOriginalFixture) ContentType() string { return "video/x-matroska" }
func (f *nativeOriginalFixture) Extension() string   { return ".mkv" }
func (f *nativeOriginalFixture) Err() error          { return f.readErr }

type nativeOriginalsFixture struct {
	admit   bool
	size    int64
	body    string
	openErr error
	opened  *nativeOriginalFixture
	marker  any
}

func (n *nativeOriginalsFixture) DownloadFile(_ context.Context, file *models.MediaFile) (*models.MediaFile, bool) {
	if !n.admit {
		return nil, false
	}
	view := *file
	view.FileSize = n.size
	return &view, true
}

func (n *nativeOriginalsFixture) OpenOriginal(ctx context.Context, _ *http.Request, _ *models.MediaFile) (NativeOriginal, error) {
	n.marker = ctx.Value(nativeServeCtxKey{})
	if n.openErr != nil {
		return nil, n.openErr
	}
	n.opened = &nativeOriginalFixture{Reader: bytes.NewReader([]byte(n.body))}
	return n.opened, nil
}

type nativeServeCtxKey struct{}

func TestNativeOriginalDownloadServesThroughNativeReader(t *testing.T) {
	native := &models.MediaFile{ID: 1, ContentID: "movie", FilePath: "bloem-storage:movie", Container: "mkv", FileSize: 12}
	local := &models.MediaFile{ID: 2, ContentID: "movie", FilePath: "/fixture/movie.mkv"}
	raw := &nativeContainmentFiles{single: native, rows: []*models.MediaFile{native, local}}
	svc := serviceWithFileRepo(GuardNativeStorageFiles(raw))
	svc.itemAccess = allowDownloadItemAccess{}
	if svc.NativeOriginalsReady() {
		t.Fatal("native originals ready before installation")
	}
	originals := &nativeOriginalsFixture{admit: true, size: 4096, body: "native-video-bytes"}
	svc.SetNativeOriginals(originals)
	if !svc.NativeOriginalsReady() {
		t.Fatal("native originals not installed")
	}

	file, err := svc.fileRepo.GetByID(t.Context(), 1)
	if err != nil || file == nil || file.FileSize != 4096 || native.FileSize != 12 {
		t.Fatalf("admitted view=%+v err=%v (catalog row must stay unchanged)", file, err)
	}
	rows, err := svc.fileRepo.GetByContentID(t.Context(), "movie")
	if err != nil || len(rows) != 2 {
		t.Fatalf("candidates=%v err=%v", rows, err)
	}

	ctx := context.WithValue(t.Context(), nativeServeCtxKey{}, "request")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=7-11")
	if err := svc.ServeDirectForProfile(ctx, rr, req, 7, "profile", 1, FormatOriginal, catalog.AccessFilter{}); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "video" {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if originals.marker != "request" {
		t.Fatal("native reader did not get the request context")
	}
	disposition := rr.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, ".mkv") || strings.Contains(disposition, "bloem-storage") {
		t.Fatalf("disposition=%q", disposition)
	}
	if rr.Header().Get("ETag") != `"native-rev"` || rr.Header().Get("Content-Type") != "video/x-matroska" || !originals.opened.closed {
		t.Fatalf("headers=%v closed=%v", rr.Header(), originals.opened.closed)
	}
}

func TestNativeOriginalDownloadRefusals(t *testing.T) {
	native := &models.MediaFile{ID: 1, ContentID: "movie", FilePath: "/library/Movie (2019)/Movie (2019).strm", Container: "mkv"}
	for _, tc := range []struct {
		name   string
		native *nativeOriginalsFixture
		want   error
	}{
		{"not installed", nil, catalog.ErrItemNotFound},
		{"not admitted", &nativeOriginalsFixture{}, catalog.ErrItemNotFound},
		{"authority refused", &nativeOriginalsFixture{admit: true, openErr: ErrDownloadNotAllowed}, ErrDownloadNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := serviceWithFileRepo(GuardNativeStorageFiles(&nativeContainmentFiles{single: native}))
			svc.itemAccess = allowDownloadItemAccess{}
			if tc.native != nil {
				svc.SetNativeOriginals(tc.native)
			}
			rr := httptest.NewRecorder()
			err := svc.ServeDirectForProfile(t.Context(), rr, httptest.NewRequest(http.MethodGet, "/", nil), 7, "", 1, FormatOriginal, catalog.AccessFilter{})
			if !errors.Is(err, tc.want) || rr.Body.Len() != 0 || rr.Header().Get("Content-Disposition") != "" {
				t.Fatalf("err=%v body=%q headers=%v", err, rr.Body.String(), rr.Header())
			}
		})
	}
}

func TestNativeOriginalDownloadReportsReadFailureAsCommitted(t *testing.T) {
	native := &models.MediaFile{ID: 1, ContentID: "movie", FilePath: "bloem-storage:movie", Container: "mkv"}
	svc := serviceWithFileRepo(GuardNativeStorageFiles(&nativeContainmentFiles{single: native}))
	svc.itemAccess = allowDownloadItemAccess{}
	originals := &nativeOriginalsFixture{admit: true, body: "partial"}
	svc.SetNativeOriginals(originals)
	failing := &failingNativeOriginals{nativeOriginalsFixture: originals}
	svc.SetNativeOriginals(failing)
	rr := httptest.NewRecorder()
	err := svc.ServeDirectForProfile(t.Context(), rr, httptest.NewRequest(http.MethodGet, "/", nil), 7, "", 1, FormatOriginal, catalog.AccessFilter{})
	if !errors.Is(err, ErrResponseCommitted) {
		t.Fatalf("err=%v", err)
	}
}

type failingNativeOriginals struct{ *nativeOriginalsFixture }

func (f *failingNativeOriginals) OpenOriginal(ctx context.Context, r *http.Request, file *models.MediaFile) (NativeOriginal, error) {
	opened, err := f.nativeOriginalsFixture.OpenOriginal(ctx, r, file)
	if err != nil {
		return nil, err
	}
	f.opened.readErr = errors.New("source revoked")
	return opened, nil
}

var _ io.ReadSeeker = (*nativeOriginalFixture)(nil)

// A native video gets the same quality decisions as a local one: an original
// downloads as it is, and a bitrate preset asks for a prepared copy, which
// download preparation reads through the native source.
func TestNativeFilesGetPreparedCopies(t *testing.T) {
	resolver := DownloadQualityResolver{}
	user := &PolicyUser{ID: 7, Policy: access.EffectiveUserPolicy{DownloadAllowed: true, DownloadTranscodeAllowed: true}}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: true}
	native := &models.MediaFile{ID: 1, FilePath: "bloem-storage:movie", Container: "mkv", CodecVideo: "h264", Resolution: "1080p"}
	if decision, err := resolver.Resolve(t.Context(), Quality5Mbps, user, cfg, native, playback.ClientCapabilities{}, true, ""); err != nil || !decision.RequiresArtifact {
		t.Fatalf("native preset decision=%+v err=%v", decision, err)
	}
	decision, err := resolver.Resolve(t.Context(), QualityOriginal, user, cfg, native, playback.ClientCapabilities{}, true, "")
	if err != nil || decision.RequiresArtifact || decision.DeliveryFormat != FormatOriginal {
		t.Fatalf("original decision=%+v err=%v", decision, err)
	}
}
