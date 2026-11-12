package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// strmSubtitleFixture binds a session to a local `.strm` item with one
// subtitle file beside it.
func strmSubtitleFixture(t *testing.T) (*StreamHandler, *models.MediaFile, *playback.Session, string) {
	t.Helper()
	service, file, session, identity, closed := nativeVideoFixture(t)
	dir := filepath.Join(t.TempDir(), "Harbor Lights (2019)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file.FilePath, file.Container = filepath.Join(dir, "Harbor Lights (2019).strm"), "mkv"
	if err := os.WriteFile(file.FilePath, []byte("https://media.example.net/harbor.mkv?token=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "Harbor Lights (2019).en.srt")
	if err := os.WriteFile(sidecar, []byte("1\n00:00:01,000 --> 00:00:02,500\nA sidecar beside the shortcut\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: sidecar, Format: "srt", Language: "eng"}}
	// As in production, storage sidecars are listed for native files; a local
	// `.strm` file keeps the subtitle files Silo's scan found beside it.
	service.Sidecars = &nativeSidecarStore{}
	*identity = nativestorage.PlaybackIdentity{FileID: file.ID, MediaFolderID: file.MediaFolderID, ContentID: file.ContentID, FilePath: file.FilePath,
		Reference: storagesource.PersistedRef{Revision: "url-sha256:one", LogicalPath: file.FilePath}}
	service.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader([]byte("0123456789")), id: *identity, info: mediasource.Info{Name: "Harbor Lights (2019).strm", LogicalPath: file.FilePath, Revision: identity.Reference.Revision, Size: 10}, closed: closed}, nil
	}
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	handler := NewStreamHandler(service.Sessions, service.Files)
	handler.NativeVideo = service
	return handler, file, session, sidecar
}

func strmSubtitleRequest(t *testing.T, handler *StreamHandler, method, sessionID, track string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	name, _, _ := strings.Cut(track, "?")
	handler.HandleSubtitle(response, playbackTestRequest(method, "/api/v2/stream/"+sessionID+"/subtitles/"+track, nil, map[string]string{"session_id": sessionID, "track": name}))
	return response
}

// A subtitle file beside a local `.strm` file is served like a local
// sidecar: WebVTT by default, the original SubRip on request, HEAD without a
// body, and never the `.strm` text.
func TestStrmSidecarSubtitlesServeLikeLocalSidecars(t *testing.T) {
	handler, _, session, _ := strmSubtitleFixture(t)
	response := strmSubtitleRequest(t, handler, http.MethodGet, session.ID, "0.vtt")
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Body.String(), "WEBVTT") || !strings.Contains(response.Body.String(), "A sidecar beside the shortcut") {
		t.Fatalf("sidecar as WebVTT: %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "token=secret") {
		t.Fatal("the subtitle response carries the .strm text")
	}
	response = strmSubtitleRequest(t, handler, http.MethodGet, session.ID, "0.srt?original=1")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "00:00:01,000 --> 00:00:02,500") {
		t.Fatalf("original sidecar: %d %q", response.Code, response.Body.String())
	}
	response = strmSubtitleRequest(t, handler, http.MethodHead, session.ID, "0.vtt")
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("sidecar HEAD: %d %q", response.Code, response.Body.String())
	}
}

// Only a subtitle file in the `.strm` file's own folder with its base name is
// served; the bound source is checked first, as for every native subtitle.
func TestStrmSidecarSubtitlesStayBesideTheirStrmFile(t *testing.T) {
	handler, file, session, sidecar := strmSubtitleFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "Harbor Lights (2019).en.srt")
	if err := os.WriteFile(elsewhere, []byte("1\n00:00:01,000 --> 00:00:02,000\nElsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherName := filepath.Join(filepath.Dir(file.FilePath), "Other Film (2020).en.srt")
	if err := os.WriteFile(otherName, []byte("1\n00:00:01,000 --> 00:00:02,000\nOther\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{elsewhere, otherName, file.FilePath} {
		file.ExternalSubtitles = []models.ExternalSubtitle{{Path: path, Format: "srt", Language: "eng"}}
		if response := strmSubtitleRequest(t, handler, http.MethodGet, session.ID, "0.vtt"); response.Code == http.StatusOK {
			t.Fatalf("served %s as a .strm sidecar: %q", path, response.Body.String())
		}
	}
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: sidecar, Format: "srt", Language: "eng"}}
	handler.NativeVideo.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return nil, storagesource.ErrReferenceConflict
	}
	if response := strmSubtitleRequest(t, handler, http.MethodGet, session.ID, "0.vtt"); response.Code == http.StatusOK {
		t.Fatal("a sidecar was served after its .strm source changed")
	}
}
