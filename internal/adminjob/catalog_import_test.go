package adminjob

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
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
// against its digest, and the checked bytes are what the import reads.
func TestBloemVerifyCatalogSeedSourceChecksTheStream(t *testing.T) {
	original := []byte("original")
	digest := fmt.Sprintf("%x", sha256.Sum256(original))
	closer := &closeRecorder{}
	var data io.ReadCloser = struct {
		io.Reader
		io.Closer
	}{bytes.NewReader(original), closer}
	if err := bloemVerifyCatalogSeedSource(&data, digest); err != nil {
		t.Fatalf("matching source rejected: %v", err)
	}
	got, err := io.ReadAll(data)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("checked source reads %q, %v; want %q", got, err, original)
	}
	if err := data.Close(); err != nil || !closer.closed {
		t.Fatalf("closing the checked source did not close the original: %v", err)
	}

	var changed io.ReadCloser = io.NopCloser(bytes.NewReader([]byte("changed")))
	if err := bloemVerifyCatalogSeedSource(&changed, digest); err == nil {
		t.Fatal("changed source accepted")
	}
}
