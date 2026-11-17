package catalogseed

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
	"testing/iotest"
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

func TestCheckBundleWithinLimitsRejectsExpandedOversize(t *testing.T) {
	data := compressedCatalogSeedPayload(t, bytes.Repeat([]byte(" "), 65))
	if err := checkBundleWithinLimits(data, int64(len(data)), 64); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
	}
}

func TestCheckBundleWithinLimitsRejectsAdditionalGzipMemberAndTrailingBytes(t *testing.T) {
	member := compressedCatalogSeedPayload(t, []byte(`{}`))
	for name, data := range map[string][]byte{
		"additional member": append(append([]byte(nil), member...), member...),
		"trailing bytes":    append(append([]byte(nil), member...), []byte("trailing")...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkBundleWithinLimits(data, int64(len(data)), 1024); !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("decode error = %v, want ErrInvalidBundle", err)
			}
		})
	}
}

// Silo's ImportReader streams the bundle; Bloem's limits still apply to it
// before Silo's decoder reads a byte.
func TestImportReaderAppliesBloemBundleLimits(t *testing.T) {
	member := compressedCatalogSeedPayload(t, []byte(`{}`))
	data := append(append([]byte(nil), member...), member...)
	if _, err := (&Service{}).ImportReader(t.Context(), bytes.NewReader(data), ImportOptions{}, nil); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("import error = %v, want ErrInvalidBundle", err)
	}
}

func TestBloemBoundedBundleReaderReportsSourceFailure(t *testing.T) {
	if _, err := bloemBoundedBundleReader(iotest.ErrReader(errors.New("cut off"))); !errors.Is(err, ErrReadingSource) {
		t.Fatalf("error = %v, want ErrReadingSource", err)
	}
}
