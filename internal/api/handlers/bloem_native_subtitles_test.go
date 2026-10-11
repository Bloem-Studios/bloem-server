package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/google/uuid"
)

func nativeSubtitleHandlerFixture(t *testing.T) (*StreamHandler, *models.MediaFile, *playback.Session, []byte) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,200 --> 00:00:01,000\nEarly native caption\n\n2\n00:00:10,400 --> 00:00:11,500\nLate native caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	font := []byte("native bundled font fixture")
	fontPath := filepath.Join(dir, "fixture.ttf")
	if err := os.WriteFile(fontPath, font, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.mkv")
	command := exec.CommandContext(t.Context(), bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=5", "-i", sub, "-map", "0:v", "-map", "1:s", "-map", "1:s", "-t", "12", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:s:0", "srt", "-c:s:1", "ass", "-attach", fontPath, "-metadata:s:t", "mimetype=application/x-truetype-font", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("subtitle fixture: %v %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, file, session, identity, closed := nativeVideoFixture(t)
	file.Container, file.Duration, file.FileSize = "mkv", 12, int64(len(data))
	file.SubtitleTracks = []models.SubtitleTrack{{Index: 1, Codec: "subrip", Language: "eng"}, {Index: 2, Codec: "ass", Language: "eng"}}
	identity.Reference.LocationID, identity.Reference.EntryID, identity.Reference.LogicalPath = uuid.New(), "subtitle-movie", "Movies/captions.mkv"
	file.FilePath, _ = storagesource.CatalogLocation(identity.Reference.LocationID, identity.Reference.EntryID)
	identity.FilePath = file.FilePath
	service.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return &videoFileFixture{Reader: bytes.NewReader(data), id: *identity, info: mediasource.Info{Name: "captions.mkv", LogicalPath: identity.Reference.LogicalPath, Revision: identity.Reference.Revision, Size: int64(len(data))}, closed: closed}, nil
	}
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	handler := NewStreamHandler(service.Sessions, service.Files)
	handler.NativeVideo = service
	handler.PlaybackConfig = func() config.PlaybackConfig { return config.PlaybackConfig{FFmpegPath: bin} }
	return handler, file, session, font
}

func TestNativeSubtitleHandlerReadsPinnedTrackAndExplicitWindow(t *testing.T) {
	handler, _, session, _ := nativeSubtitleHandlerFixture(t)
	for _, item := range []struct {
		query       string
		late, early bool
	}{{"", true, true}, {"?position=9&duration=3", true, false}} {
		response := httptest.NewRecorder()
		request := playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt"+item.query, nil, map[string]string{"session_id": session.ID, "track": "0.vtt"})
		handler.HandleSubtitle(response, request)
		if response.Code != http.StatusOK || !strings.HasPrefix(response.Body.String(), "WEBVTT") || strings.Contains(response.Body.String(), "Late native caption") != item.late || strings.Contains(response.Body.String(), "Early native caption") != item.early {
			t.Fatalf("native subtitle window: %d %s", response.Code, response.Body.String())
		}
		if item.query != "" && !strings.Contains(response.Body.String(), "00:10.400") {
			t.Fatalf("window rebased original cue timestamps: %s", response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("native HEAD: %d %s", response.Code, response.Body.String())
	}
}

func TestNativeSubtitleFontsReadFreshPinnedAttachments(t *testing.T) {
	handler, _, session, font := nativeSubtitleHandlerFixture(t)
	fonts, err := handler.SubtitleFonts(newAuthorizedPlaybackContext(), SubtitleFontRequest{SessionID: session.ID, Track: "1.ass", Query: make(url.Values)})
	if err != nil || len(fonts) != 1 {
		t.Fatalf("native fonts: %v count %d", err, len(fonts))
	}
	decoded, err := base64.StdEncoding.DecodeString(fonts[0].Data)
	if err != nil || !bytes.Equal(decoded, font) {
		t.Fatal("native font bytes changed")
	}
}

func TestNativeSubtitleRefusesExternalSidecarsAndReplacement(t *testing.T) {
	handler, file, session, _ := nativeSubtitleHandlerFixture(t)
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: filepath.Join(t.TempDir(), "private.srt"), Format: "srt", Language: "eng"}}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code == http.StatusOK {
		t.Fatal("native external sidecar admitted")
	}
	handler.NativeVideo.Open = func(context.Context, *models.MediaFile) (NativeVideoFile, error) {
		return nil, storagesource.ErrReferenceConflict
	}
	response = httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/1.vtt", nil, map[string]string{"session_id": session.ID, "track": "1.vtt"}))
	if response.Code == http.StatusOK {
		t.Fatal("stale native subtitle source admitted")
	}
}

func TestNativeSubtitleSignedRecoveryChecksSourceBeforeRegistration(t *testing.T) {
	handler, file, session, _ := nativeSubtitleHandlerFixture(t)
	registry := playback.NewSessionManager(0, 0)
	handler.sessionMgr = registry
	handler.NativeVideo.Sessions = registry
	playbackHandler := NewPlaybackHandler(registry, handler.NativeVideo.Files)
	playbackHandler.NativeVideo = handler.NativeVideo
	playbackHandler.InstallNativeVideoRecovery()
	handler.TM = playbackHandler.tm
	handler.JWTSecret = "native-subtitle-test-secret"
	card := playback.NewDirectRecipeCard(session.ID, session.UserID, session.ProfileID, file.ID)
	card.InputPath = file.FilePath
	token, err := streamtoken.Sign(card.ToClaims(), handler.JWTSecret, playback.MaxTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt?st="+url.QueryEscape(token), nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("signed native recovery: %d %s", response.Code, response.Body.String())
	}
	if _, err := registry.GetSession(session.ID); err != nil {
		t.Fatal("authorized native sidecar did not reconstruct session")
	}
}

func TestNativeSubtitleStableStreamIdentitySurvivesExternalInsertion(t *testing.T) {
	handler, file, session, _ := nativeSubtitleHandlerFixture(t)
	file.ExternalSubtitles = []models.ExternalSubtitle{{Path: "unread-native-sidecar.srt", Format: "srt"}}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt?embedded_stream_index=1", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Late native caption") {
		t.Fatalf("stable native identity: %d %s", response.Code, response.Body.String())
	}
	for _, query := range []string{"embedded_stream_index=1&embedded_stream_index=2", "embedded_stream_index=99"} {
		response := httptest.NewRecorder()
		handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt?"+query, nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
		if response.Code == http.StatusOK {
			t.Fatalf("invalid stream identity admitted: %s", query)
		}
	}
}

type nativeSubtitleRevokingFile struct {
	NativeVideoFile
	revoked atomic.Bool
}

func (f *nativeSubtitleRevokingFile) ReadAt(p []byte, offset int64) (int, error) {
	n, err := f.NativeVideoFile.ReadAt(p, offset)
	f.revoked.Store(true)
	return n, err
}
func (f *nativeSubtitleRevokingFile) Reauthorize(ctx context.Context) error {
	if f.revoked.Load() {
		return storagesource.ErrReferenceConflict
	}
	return f.NativeVideoFile.Reauthorize(ctx)
}
func TestNativeSubtitleLateSourceFailureDiscardsAllOutput(t *testing.T) {
	handler, _, session, _ := nativeSubtitleHandlerFixture(t)
	handler.NativeVideo.OpenDelegated = func(ctx context.Context, file *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
		opened, err := handler.NativeVideo.Open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &nativeSubtitleRevokingFile{NativeVideoFile: opened}, nil
	}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodGet, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "WEBVTT") || strings.Contains(response.Body.String(), "native caption") {
		t.Fatalf("failed source exposed partial subtitle: %d %s", response.Code, response.Body.String())
	}
}

type nativeSubtitleNoReadFile struct {
	NativeVideoFile
	reads *atomic.Int64
}

func (f *nativeSubtitleNoReadFile) ReadAt([]byte, int64) (int, error) {
	f.reads.Add(1)
	return 0, errors.New("unexpected HEAD provider Read")
}
func TestNativeSubtitleTextHEADValidatesWithoutReadingBytes(t *testing.T) {
	handler, _, session, _ := nativeSubtitleHandlerFixture(t)
	open := handler.NativeVideo.Open
	reads := new(atomic.Int64)
	handler.NativeVideo.Open = func(ctx context.Context, file *models.MediaFile) (NativeVideoFile, error) {
		opened, err := open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &nativeSubtitleNoReadFile{NativeVideoFile: opened, reads: reads}, nil
	}
	response := httptest.NewRecorder()
	handler.HandleSubtitle(response, playbackTestRequest(http.MethodHead, "/api/v1/stream/"+session.ID+"/subtitles/0.vtt", nil, map[string]string{"session_id": session.ID, "track": "0.vtt"}))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || reads.Load() != 0 {
		t.Fatalf("HEAD read source bytes: %d reads=%d", response.Code, reads.Load())
	}
}

func TestNativeSubtitleFontsRefuseLateSourceFailure(t *testing.T) {
	handler, _, session, _ := nativeSubtitleHandlerFixture(t)
	handler.NativeVideo.OpenDelegated = func(ctx context.Context, file *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
		opened, err := handler.NativeVideo.Open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &nativeSubtitleRevokingFile{NativeVideoFile: opened}, nil
	}
	fonts, err := handler.SubtitleFonts(newAuthorizedPlaybackContext(), SubtitleFontRequest{SessionID: session.ID, Track: "1.ass", Query: make(url.Values)})
	if err == nil || len(fonts) != 0 {
		t.Fatal("failed source exposed a font bundle")
	}
}
