package catalogseed

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// Bloem: bounded catalog-seed decoding. Silo's decodeBundle reads an
// unbounded gzip stream; Bloem caps both sizes and rejects extra members.

const (
	MaxCompressedBundleBytes int64 = 64 << 20
	MaxExpandedBundleBytes   int64 = 512 << 20
)

// ValidateBundle verifies that data is one bounded catalog-seed gzip member.
func ValidateBundle(data []byte) error {
	return checkBundleWithinLimits(data, MaxCompressedBundleBytes, MaxExpandedBundleBytes)
}

// bloemBoundedBundleReader reads at most MaxCompressedBundleBytes from r,
// checks the bundle's limits, and returns a reader over the checked bytes for
// Silo's decoder. A failure of r itself is reported as ErrReadingSource, as
// Silo's decoder reports it.
func bloemBoundedBundleReader(r io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxCompressedBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReadingSource, err)
	}
	if err := checkBundleWithinLimits(data, MaxCompressedBundleBytes, MaxExpandedBundleBytes); err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// checkBundleWithinLimits verifies the compressed and expanded sizes and that
// data holds exactly one gzip member, without keeping the expanded bytes.
func checkBundleWithinLimits(data []byte, maxCompressed, maxExpanded int64) error {
	if int64(len(data)) > maxCompressed {
		return fmt.Errorf("%w: compressed catalog seed exceeds %d bytes", ErrInvalidBundle, maxCompressed)
	}
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("%w: opening catalog seed bundle: %w", ErrInvalidBundle, err)
	}
	defer gz.Close()
	gz.Multistream(false)

	expanded, err := io.Copy(io.Discard, io.LimitReader(gz, maxExpanded+1))
	if err != nil {
		return fmt.Errorf("%w: reading catalog seed bundle: %w", ErrInvalidBundle, err)
	}
	if expanded > maxExpanded {
		return fmt.Errorf("%w: expanded catalog seed exceeds %d bytes", ErrInvalidBundle, maxExpanded)
	}
	if compressed.Len() != 0 {
		return fmt.Errorf("%w: catalog seed contains additional gzip members or trailing data", ErrInvalidBundle)
	}
	return nil
}

