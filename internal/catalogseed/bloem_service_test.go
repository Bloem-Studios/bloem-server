package catalogseed

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
)

func compressedCatalogSeedPayload(t *testing.T, payload []byte) []byte {
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

func TestDecodeBundleWithinLimitsRejectsExpandedOversize(t *testing.T) {
	data := compressedCatalogSeedPayload(t, bytes.Repeat([]byte(" "), 65))
	if _, err := decodeBundleWithinLimits(data, int64(len(data)), 64); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
	}
}

func TestDecodeBundleWithinLimitsRejectsAdditionalGzipMemberAndTrailingBytes(t *testing.T) {
	member := compressedCatalogSeedPayload(t, []byte(`{}`))
	for name, data := range map[string][]byte{
		"additional member": append(append([]byte(nil), member...), member...),
		"trailing bytes":    append(append([]byte(nil), member...), []byte("trailing")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBundleWithinLimits(data, int64(len(data)), 1024); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}
