package storagesource

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// CatalogLocation is an internal identity, never a filename to open or a URL.
// Revisions deliberately do not participate in the catalog identity.
func CatalogLocation(locationID uuid.UUID, entryID string) (string, error) {
	if locationID == uuid.Nil || !validText(entryID, 1024, true) {
		return "", fmt.Errorf("invalid storage catalog identity")
	}
	data := make([]byte, 20+len(entryID))
	copy(data, locationID[:])
	binary.BigEndian.PutUint32(data[16:20], uint32(len(entryID)))
	copy(data[20:], entryID)
	digest := sha256.Sum256(data)
	return "bloem-storage:" + hex.EncodeToString(digest[:]), nil
}
