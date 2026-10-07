package artworkkey

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// StorageCoverScheme prefixes the poster path of a book whose cover its
// storage source serves on demand. The cover is never copied into artwork
// storage: the path names it, and the signed artwork route reads it through
// the source's plugin when a client asks.
//
//	bloem-storage://storage-covers/<content_id>/original.<revision>.img
const StorageCoverScheme = "bloem-storage"

const storageCoverPrefix = "storage-covers/"

// StorageCoverKey is the artwork route key of a book's storage cover. Its
// revision hashes the provider's cover entry and revision, so a changed cover
// gets a new URL and a cached one never goes stale.
func StorageCoverKey(contentID, coverEntryID, coverRevision string) string {
	if contentID == "" || strings.Contains(contentID, "/") || coverEntryID == "" || coverRevision == "" {
		return ""
	}
	return storageCoverPrefix + contentID + "/" + OriginalVariant + "." + StorageCoverRevision(coverEntryID, coverRevision) + ".img"
}

// StorageCoverPath is the poster path that names a book's storage cover.
func StorageCoverPath(contentID, coverEntryID, coverRevision string) string {
	key := StorageCoverKey(contentID, coverEntryID, coverRevision)
	if key == "" {
		return ""
	}
	return StorageCoverScheme + "://" + key
}

// StorageCoverRevision hashes a provider cover identity. Migration
// 20261007220212_storage_covers_on_demand computes the same value in SQL.
func StorageCoverRevision(coverEntryID, coverRevision string) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(len(coverEntryID)) + ":" + coverEntryID + coverRevision))
	return hex.EncodeToString(sum[:16])
}

// IsStorageCoverPath reports whether a poster path names a storage cover.
func IsStorageCoverPath(path string) bool {
	return strings.HasPrefix(path, StorageCoverScheme+"://"+storageCoverPrefix)
}

// ParseStorageCoverKey returns the content ID and revision a storage cover key
// names.
func ParseStorageCoverKey(key string) (contentID, revision string, ok bool) {
	rest, ok := strings.CutPrefix(key, storageCoverPrefix)
	if !ok {
		return "", "", false
	}
	contentID, name, ok := strings.Cut(rest, "/")
	if !ok || contentID == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	revision, ok = strings.CutPrefix(name, OriginalVariant+".")
	if !ok {
		return "", "", false
	}
	revision, ok = strings.CutSuffix(revision, ".img")
	if !ok || len(revision) != 32 {
		return "", "", false
	}
	return contentID, revision, true
}
