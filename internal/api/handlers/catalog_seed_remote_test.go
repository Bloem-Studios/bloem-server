package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalogseed"
	"github.com/Silo-Server/silo-server/internal/outbound"
)

func compressedCatalogSeedForHandlerTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestFetchRemoteCatalogSeedRejectsPrivateDestinationBeforeRequest(t *testing.T) {
	client := outbound.NewClient(outbound.PublicHTTPPolicy(), outbound.WithTimeout(time.Second))
	_, err := fetchRemoteCatalogSeed(context.Background(), client, "http://127.0.0.1/catalog.json.gz")
	if err == nil {
		t.Fatal("fetch succeeded, want private destination rejection")
	}
}

func TestFetchRemoteCatalogSeedFetchesExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	payload := compressedCatalogSeedForHandlerTest(t, []byte(`{}`))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	policy := outbound.PublicHTTPPolicy()
	policy.AllowPrivate = true
	client := outbound.NewClient(policy, outbound.WithTimeout(time.Second))
	if _, err := fetchRemoteCatalogSeed(context.Background(), client, server.URL+"/catalog.json.gz"); err != nil {
		t.Fatalf("fetchRemoteCatalogSeed: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestFetchRemoteCatalogSeedRejectsOversizedResponseBeforeReading(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(catalogseed.MaxCompressedBundleBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	policy := outbound.PublicHTTPPolicy()
	policy.AllowPrivate = true
	client := outbound.NewClient(policy, outbound.WithTimeout(time.Second))
	if _, err := fetchRemoteCatalogSeed(context.Background(), client, server.URL+"/catalog.json.gz"); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestRemoteCatalogSeedLabelDropsQueryCredentials(t *testing.T) {
	got := remoteCatalogSeedLabel("https://catalog.example/seed.json.gz?token=secret")
	if got != "https://catalog.example/seed.json.gz" {
		t.Fatalf("label = %q", got)
	}
}

type recordingCatalogSeedStagingStore struct {
	keys  []string
	bytes [][]byte
}

func (s *recordingCatalogSeedStagingStore) Bucket() string { return "internal" }

func (s *recordingCatalogSeedStagingStore) UploadFile(_ context.Context, bucket, key, path, contentType string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if bucket != "internal" || contentType != "application/gzip" {
		return 0, errors.New("unexpected staging metadata")
	}
	s.keys = append(s.keys, key)
	s.bytes = append(s.bytes, data)
	return int64(len(data)), nil
}

func TestStageRemoteCatalogSeedCreatesUniqueDigestBoundArtifacts(t *testing.T) {
	store := &recordingCatalogSeedStagingStore{}
	data := compressedCatalogSeedForHandlerTest(t, []byte(`{}`))
	_, firstKey, firstDigest, err := stageRemoteCatalogSeed(context.Background(), store, data)
	if err != nil {
		t.Fatal(err)
	}
	_, secondKey, secondDigest, err := stageRemoteCatalogSeed(context.Background(), store, data)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatalf("staged keys are equal: %q", firstKey)
	}
	if firstDigest == "" || firstDigest != secondDigest {
		t.Fatalf("digests = %q and %q, want equal non-empty", firstDigest, secondDigest)
	}
	if !bytes.Equal(store.bytes[0], data) || !bytes.Equal(store.bytes[1], data) {
		t.Fatal("staged bytes differ from fetched bytes")
	}
}
