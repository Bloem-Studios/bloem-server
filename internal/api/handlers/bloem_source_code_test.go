package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemsource"
	"github.com/Silo-Server/silo-server/internal/buildinfo"
)

const sourceTestRevision = "0123456789abcdef0123456789abcdef01234567"

func sourceCodeHandler(t *testing.T, content string) *BloemSourceCodeHandler {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		path := filepath.Join(dir, bloemsource.ArchiveName(sourceTestRevision))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := NewBloemSourceCodeHandler(bloemsource.New(dir, buildinfo.Info{Revision: sourceTestRevision}))
	if !h.Available() {
		t.Fatal("handler with an offer is not available")
	}
	return h
}

func TestBloemSourceCodeHandlerNeedsAnOffer(t *testing.T) {
	if h := NewBloemSourceCodeHandler(nil); h != nil || h.Available() {
		t.Fatalf("NewBloemSourceCodeHandler(nil) = %+v, want nil and unavailable", h)
	}
}

func TestBloemSourceCodeDescribesTheRunningVersion(t *testing.T) {
	h := sourceCodeHandler(t, "archive bytes")
	rec := httptest.NewRecorder()
	h.HandleGet(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/source-code", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status %d headers %v body %s", rec.Code, rec.Header(), rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"license":      "AGPL-3.0",
		"revision":     sourceTestRevision,
		"archive_name": "bloem-server-" + sourceTestRevision + ".tar.gz",
		"archive_path": "/api/bloem/v2/source-code/archive",
		"size_bytes":   float64(len("archive bytes")),
	}
	if len(body) != len(want) {
		t.Fatalf("body %v, want exactly %v", body, want)
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %v, want %v", key, body[key], value)
		}
	}
}

func TestBloemSourceCodeArchiveDownloads(t *testing.T) {
	h := sourceCodeHandler(t, "archive bytes")
	rec := httptest.NewRecorder()
	h.HandleArchive(rec, httptest.NewRequest(http.MethodGet, SourceCodeArchivePath, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "archive bytes" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	headers := map[string]string{
		"Content-Type":           "application/gzip",
		"Content-Disposition":    "attachment; filename=bloem-server-" + sourceTestRevision + ".tar.gz",
		"Cache-Control":          "private, no-store",
		"X-Content-Type-Options": "nosniff",
		"Accept-Ranges":          "bytes",
	}
	for name, value := range headers {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestBloemSourceCodeArchiveResumes(t *testing.T) {
	h := sourceCodeHandler(t, "archive bytes")
	req := httptest.NewRequest(http.MethodGet, SourceCodeArchivePath, nil)
	req.Header.Set("Range", "bytes=8-")
	rec := httptest.NewRecorder()
	h.HandleArchive(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "bytes" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
}

func TestBloemSourceCodeArchiveOverAServer(t *testing.T) {
	h := sourceCodeHandler(t, "archive bytes")
	server := httptest.NewServer(http.HandlerFunc(h.HandleArchive))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "archive bytes" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
}

func TestBloemSourceCodeMissingArchiveIsUnavailable(t *testing.T) {
	h := sourceCodeHandler(t, "")
	for _, handle := range []http.HandlerFunc{h.HandleGet, h.HandleArchive} {
		rec := httptest.NewRecorder()
		handle(rec, httptest.NewRequest(http.MethodGet, SourceCodeArchivePath, nil))
		var body errorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusServiceUnavailable || body.Error != "source_unavailable" {
			t.Fatalf("status %d body %s", rec.Code, rec.Body)
		}
	}
}

func TestBloemSourceCodeCapabilityTracksWiring(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := NewBloemSystemHandler(nil)
		h.SetSourceCodeAvailable(enabled)
		rec := httptest.NewRecorder()
		h.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/capabilities", nil))
		var body struct {
			FeatureTokens []string `json:"feature_tokens"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(body.FeatureTokens, SourceCodeCapabilityToken) != enabled {
			t.Fatalf("capability enabled=%v body=%s", enabled, rec.Body.String())
		}
	}
}
