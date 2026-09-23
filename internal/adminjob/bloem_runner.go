package adminjob

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

func verifyCatalogSeedDigest(data []byte, expected string) error {
	if expected == "" {
		return nil
	}
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("catalog import source digest is invalid")
	}
	actual := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(actual[:], want) != 1 {
		return fmt.Errorf("catalog import source digest mismatch")
	}
	return nil
}
