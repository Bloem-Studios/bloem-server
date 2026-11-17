package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/outbound"
)

type stagingCatalogSeedArtifactStore struct {
	CatalogSeedArtifactStore
	recordingCatalogSeedStagingStore
}

func (s *stagingCatalogSeedArtifactStore) Bucket() string {
	return s.recordingCatalogSeedStagingStore.Bucket()
}

func (s *stagingCatalogSeedArtifactStore) UploadFile(ctx context.Context, bucket, key, path, contentType string) (int64, error) {
	return s.recordingCatalogSeedStagingStore.UploadFile(ctx, bucket, key, path, contentType)
}

func (s *stagingCatalogSeedArtifactStore) DeleteObject(context.Context, string, string) error {
	return nil
}

// Silo's CreateCatalogImportJob sets RemoteURL, and the job runner reads
// RemoteURL before a staged object. Staging must clear it, so the job reads
// the staged copy and never fetches the admin-supplied URL itself.
func TestBloemStageRemoteCatalogSeedLeavesNoURLForTheJob(t *testing.T) {
	payload := compressedCatalogSeedForHandlerTest(t, []byte(`{}`))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	policy := outbound.PublicHTTPPolicy()
	policy.AllowPrivate = true
	client := outbound.NewClient(policy, outbound.WithTimeout(time.Second))
	defaultClient := bloemCatalogSeedRemoteClient
	bloemCatalogSeedRemoteClient = func() *outbound.Client { return client }
	t.Cleanup(func() { bloemCatalogSeedRemoteClient = defaultClient })
	h := &CatalogSeedHandler{store: &stagingCatalogSeedArtifactStore{}}
	remote := server.URL + "/catalog.json.gz"
	req := adminjob.CatalogImportRequest{RemoteURL: remote, SourceLabel: remote}
	if err := h.bloemStageRemoteCatalogSeed(t.Context(), remote, &req); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if req.RemoteURL != "" {
		t.Fatalf("RemoteURL = %q after staging, want empty", req.RemoteURL)
	}
	digest := sha256.Sum256(payload)
	if !strings.HasSuffix(req.SourceKey, "/"+hex.EncodeToString(digest[:])+".json.gz") || !req.CleanupSource {
		t.Fatalf("staged request = %+v, want a staged source whose key names its digest", req)
	}
}

// The default remote seed client refuses private destinations, so an admin
// URL cannot reach the server's own network.
func TestBloemCatalogSeedRemoteClientRejectsPrivateDestinations(t *testing.T) {
	payload := compressedCatalogSeedForHandlerTest(t, []byte(`{}`))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	_, err := fetchRemoteCatalogSeed(t.Context(), bloemCatalogSeedRemoteClient(), server.URL+"/catalog.json.gz")
	if err == nil || requests.Load() != 0 {
		t.Fatalf("fetch error = %v after %d requests, want a private destination refusal before any request", err, requests.Load())
	}
}
