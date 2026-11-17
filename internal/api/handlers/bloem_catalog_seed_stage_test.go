package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/outbound"
)

type stagingCatalogSeedArtifactStore struct {
	CatalogSeedArtifactStore
	recordingCatalogSeedStagingStore
}

func (s *stagingCatalogSeedArtifactStore) Bucket() string { return s.recordingCatalogSeedStagingStore.Bucket() }

func (s *stagingCatalogSeedArtifactStore) UploadFile(ctx context.Context, bucket, key, path, contentType string) (int64, error) {
	return s.recordingCatalogSeedStagingStore.UploadFile(ctx, bucket, key, path, contentType)
}

func (s *stagingCatalogSeedArtifactStore) DeleteObject(context.Context, string, string) error { return nil }

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
	h := &CatalogSeedHandler{
		store:        &stagingCatalogSeedArtifactStore{},
		remoteClient: outbound.NewClient(policy, outbound.WithTimeout(time.Second)),
	}
	remote := server.URL + "/catalog.json.gz"
	req := adminjob.CatalogImportRequest{RemoteURL: remote, SourceLabel: remote}
	if err := h.bloemStageRemoteCatalogSeed(t.Context(), remote, &req); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if req.RemoteURL != "" {
		t.Fatalf("RemoteURL = %q after staging, want empty", req.RemoteURL)
	}
	if req.SourceKey == "" || req.SourceSHA256 == "" || !req.CleanupSource {
		t.Fatalf("staged request = %+v, want a staged source", req)
	}
}
