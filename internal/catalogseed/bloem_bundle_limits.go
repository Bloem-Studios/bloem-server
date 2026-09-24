package catalogseed

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
	_, err := decodeBundleWithinLimits(data, MaxCompressedBundleBytes, MaxExpandedBundleBytes)
	return err
}

func decodeBundleWithinLimits(data []byte, maxCompressed, maxExpanded int64) (*Bundle, error) {
	if int64(len(data)) > maxCompressed {
		return nil, fmt.Errorf("%w: compressed catalog seed exceeds %d bytes", ErrInvalidBundle, maxCompressed)
	}
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("%w: opening catalog seed bundle: %w", ErrInvalidBundle, err)
	}
	defer gz.Close()
	gz.Multistream(false)

	payload, err := io.ReadAll(io.LimitReader(gz, maxExpanded+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading catalog seed bundle: %w", ErrInvalidBundle, err)
	}
	if int64(len(payload)) > maxExpanded {
		return nil, fmt.Errorf("%w: expanded catalog seed exceeds %d bytes", ErrInvalidBundle, maxExpanded)
	}
	if compressed.Len() != 0 {
		return nil, fmt.Errorf("%w: catalog seed contains additional gzip members or trailing data", ErrInvalidBundle)
	}

	var bundle Bundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return nil, fmt.Errorf("%w: decoding catalog seed bundle: %w", ErrInvalidBundle, err)
	}
	return &bundle, nil
}
