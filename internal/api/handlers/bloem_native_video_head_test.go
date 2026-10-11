package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type nativeVideoHeadFile struct {
	NativeVideoFile
	reads, checks *atomic.Int32
}

func (f *nativeVideoHeadFile) Read(p []byte) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.Read(p)
}
func (f *nativeVideoHeadFile) ReadAt(p []byte, offset int64) (int, error) {
	f.reads.Add(1)
	return f.NativeVideoFile.ReadAt(p, offset)
}
func (f *nativeVideoHeadFile) Reauthorize(ctx context.Context) error {
	f.checks.Add(1)
	return f.NativeVideoFile.Reauthorize(ctx)
}

func TestNativeVideoProgressiveHEADDoesNotRunFFmpegOrReadSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable process sentinel uses a Unix shell")
	}
	service, file, session, identity, closed := nativeVideoFixture(t)
	if err := service.Bind(t.Context(), session, file); err != nil {
		t.Fatal(err)
	}
	reads, checks := &atomic.Int32{}, &atomic.Int32{}
	service.OpenDelegated = func(ctx context.Context, file *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
		opened, err := service.Open(ctx, file)
		if err != nil {
			return nil, err
		}
		return &nativeVideoHeadFile{NativeVideoFile: opened, reads: reads, checks: checks}, nil
	}
	marker := filepath.Join(t.TempDir(), "process-started")
	bin := filepath.Join(t.TempDir(), "ffmpeg-sentinel")
	t.Setenv("BLOEM_TEST_HEAD_PROCESS_MARKER", marker)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf started > \"$BLOEM_TEST_HEAD_PROCESS_MARKER\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := NewStreamHandler(service.Sessions, service.Files)
	h.NativeVideo = service
	h.PlaybackConfig = func() config.PlaybackConfig { return config.PlaybackConfig{FFmpegPath: bin} }
	session.PlayMethod = playback.PlayRemux
	w := httptest.NewRecorder()
	err := h.serveNativeVideoRemux(w, httptest.NewRequest(http.MethodHead, "/stream/"+session.ID+"?seek=2.5", nil), session, file)
	_, processErr := os.Stat(marker)
	if err != nil || !errors.Is(processErr, os.ErrNotExist) || reads.Load() != 0 {
		t.Fatalf("HEAD ran media work: error=%v process=%v provider reads=%d", err, processErr, reads.Load())
	}
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("HEAD response: status=%d body=%d headers=%v", w.Code, w.Body.Len(), w.Header())
	}
	if checks.Load() == 0 || *closed != 2 {
		t.Fatalf("HEAD did not freshly authorize and join source: checks=%d closes=%d", checks.Load(), *closed)
	}
	identity.Reference.Revision = "changed-same-size-revision"
	denied := httptest.NewRecorder()
	if err := h.serveNativeVideoRemux(denied, httptest.NewRequest(http.MethodHead, "/stream/"+session.ID, nil), session, file); !errors.Is(err, storagesource.ErrReferenceConflict) {
		t.Fatalf("HEAD accepted changed revision: %v", err)
	}
	if denied.Body.Len() != 0 || reads.Load() != 0 {
		t.Fatal("changed revision exposed bytes")
	}
}
