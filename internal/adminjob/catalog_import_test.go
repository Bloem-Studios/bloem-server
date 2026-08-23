package adminjob

import (
	"crypto/sha256"
	"fmt"
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
