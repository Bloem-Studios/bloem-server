package adminjob

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestVerifyCatalogSeedDigestRejectsChangedArtifact(t *testing.T) {
	original := []byte("original")
	digest := fmt.Sprintf("%x", sha256.Sum256(original))
	if err := verifyCatalogSeedDigest([]byte("changed"), digest); err == nil {
		t.Fatal("changed artifact accepted")
	}
}

func TestVerifyCatalogSeedDigestAllowsUnpinnedExistingSource(t *testing.T) {
	if err := verifyCatalogSeedDigest([]byte("existing source"), ""); err != nil {
		t.Fatalf("unpinned existing source rejected: %v", err)
	}
}

type closeRecorder struct{ closed bool }

func (c *closeRecorder) Close() error { c.closed = true; return nil }

// Silo's catalog import streams its source; a staged source is still checked
// against the digest its key names, and the checked bytes are what the import
// reads.
func TestBloemVerifyCatalogSeedSourceChecksTheStream(t *testing.T) {
	original := []byte("original")
	key := BloemStagedCatalogSeedKey([16]byte{1}, fmt.Sprintf("%x", sha256.Sum256(original)))
	closer := &closeRecorder{}
	var data io.ReadCloser = struct {
		io.Reader
		io.Closer
	}{bytes.NewReader(original), closer}
	if err := bloemVerifyCatalogSeedSource(&data, key); err != nil {
		t.Fatalf("matching source rejected: %v", err)
	}
	got, err := io.ReadAll(data)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("checked source reads %q, %v; want %q", got, err, original)
	}
	if err := data.Close(); err != nil || !closer.closed {
		t.Fatalf("closing the checked source did not close the original: %v", err)
	}

	changed := io.NopCloser(bytes.NewReader([]byte("changed")))
	if err := bloemVerifyCatalogSeedSource(&changed, key); err == nil {
		t.Fatal("changed source accepted")
	}
}

// A source whose key names no digest, such as an uploaded seed, is read as
// Silo reads it; a key that only resembles a staged one is not trusted to.
func TestBloemVerifyCatalogSeedSourceSkipsUnstagedKeys(t *testing.T) {
	for _, key := range []string{
		"",
		"catalog-seeds/uploads/seed.json.gz",
		"catalog-seeds/imports/01/" + strings.Repeat("a", 64) + ".json.gz",
		"x/catalog-seeds/imports/" + strings.Repeat("0", 32) + "/" + strings.Repeat("a", 64) + ".json.gz",
	} {
		var data io.ReadCloser = io.NopCloser(bytes.NewReader([]byte("anything")))
		if err := bloemVerifyCatalogSeedSource(&data, key); err != nil {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	staged := BloemStagedCatalogSeedKey([16]byte{}, strings.Repeat("a", 64))
	if got := bloemStagedCatalogSeedDigest(staged); got != strings.Repeat("a", 64) {
		t.Fatalf("digest of %q = %q", staged, got)
	}
}
