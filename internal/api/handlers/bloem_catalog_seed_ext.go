package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/catalogseed"
	"github.com/Silo-Server/silo-server/internal/outbound"
)

type catalogSeedStagingStore interface {
	Bucket() string
	UploadFile(ctx context.Context, bucket, key, path, contentType string) (int64, error)
}

func fetchRemoteCatalogSeed(ctx context.Context, client *outbound.Client, remoteURL string) ([]byte, error) {
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return nil, errCatalogSeedImportInvalidRemoteURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errCatalogSeedImportInvalidRemoteURL
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Path), ".json.gz") {
		return nil, errCatalogSeedImportInvalidRemoteURL
	}

	if client == nil {
		return nil, fmt.Errorf("remote catalog seed client is not configured")
	}
	response, err := client.Fetch(ctx, outbound.Request{
		URL:      remoteURL,
		MaxBytes: catalogseed.MaxCompressedBundleBytes,
		Statuses: map[int]struct{}{http.StatusOK: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("downloading remote catalog seed: %w", err)
	}
	if response.FinalURL == nil || !strings.HasSuffix(strings.ToLower(response.FinalURL.Path), ".json.gz") {
		return nil, errCatalogSeedImportInvalidRemoteURL
	}
	if err := catalogseed.ValidateBundle(response.Body); err != nil {
		return nil, err
	}
	return response.Body, nil
}

func remoteCatalogSeedLabel(remoteURL string) string {
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return "remote catalog seed"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

func stageRemoteCatalogSeed(ctx context.Context, store catalogSeedStagingStore, data []byte) (string, string, string, error) {
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", "", fmt.Errorf("generating staged catalog seed key: %w", err)
	}
	key := fmt.Sprintf("%simports/%x/%s.json.gz", catalogSeedImportPrefix, nonce, digestHex)
	temp, err := os.CreateTemp("", "bloem-catalog-seed-*.json.gz")
	if err != nil {
		return "", "", "", fmt.Errorf("creating staged catalog seed file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", "", "", fmt.Errorf("writing staged catalog seed file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", "", "", fmt.Errorf("closing staged catalog seed file: %w", err)
	}
	bucket := store.Bucket()
	if _, err := store.UploadFile(ctx, bucket, key, tempPath, "application/gzip"); err != nil {
		return "", "", "", fmt.Errorf("uploading staged catalog seed: %w", err)
	}
	return bucket, key, digestHex, nil
}

// withBloemCatalogSeedRemoteClient installs the SSRF-guarded outbound client
// used for remote catalog seed downloads.
func withBloemCatalogSeedRemoteClient(h *CatalogSeedHandler) *CatalogSeedHandler {
	h.remoteClient = outbound.NewClient(outbound.PublicHTTPPolicy(), outbound.WithTimeout(remoteCatalogSeedTimeout))
	return h
}

// catalogSeedCleanupStagingStore is the artifact store a remote catalog seed
// is staged into and cleaned up from.
type catalogSeedCleanupStagingStore interface {
	catalogSeedStagingStore
	DeleteObject(context.Context, string, string) error
}

// bloemStageRemoteCatalogSeed downloads a remote catalog seed through the
// guarded outbound client and stages it in the artifact store, so the import
// job never fetches an admin-supplied URL itself.
func (h *CatalogSeedHandler) bloemStageRemoteCatalogSeed(ctx context.Context, remoteURL string, req *adminjob.CatalogImportRequest) error {
	if h.store == nil {
		return catalogImportSourceProblem(errCatalogSeedImportSourceUnavailable)
	}
	staging, ok := h.store.(catalogSeedCleanupStagingStore)
	if !ok {
		return catalogImportSourceProblem(errCatalogSeedImportSourceUnavailable)
	}
	data, err := fetchRemoteCatalogSeed(ctx, h.remoteClient, remoteURL)
	if err != nil {
		return catalogImportSourceProblem(err)
	}
	bucket, key, digest, err := stageRemoteCatalogSeed(ctx, staging, data)
	if err != nil {
		return apiError(500, "internal_error", "Failed to stage catalog seed source")
	}
	req.SourceBucket, req.SourceKey, req.SourceSHA256 = bucket, key, digest
	req.SourceLabel, req.CleanupSource = remoteCatalogSeedLabel(remoteURL), true
	return nil
}

// bloemCleanupStagedCatalogSeed deletes a staged remote seed when the import
// job could not be created.
func (h *CatalogSeedHandler) bloemCleanupStagedCatalogSeed(ctx context.Context, createErr error, req adminjob.CatalogImportRequest) {
	if createErr == nil || !req.CleanupSource {
		return
	}
	if staging, ok := h.store.(catalogSeedCleanupStagingStore); ok {
		_ = staging.DeleteObject(context.WithoutCancel(ctx), req.SourceBucket, req.SourceKey)
	}
}
