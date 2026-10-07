package artworkkey

import "testing"

func TestStorageCoverKeyRoundTrip(t *testing.T) {
	key := StorageCoverKey("146532612416483348", "cover/book-1", "cover:abc")
	contentID, revision, ok := ParseStorageCoverKey(key)
	if !ok || contentID != "146532612416483348" || revision != StorageCoverRevision("cover/book-1", "cover:abc") {
		t.Fatalf("parse %q = %q %q %v", key, contentID, revision, ok)
	}
	if Revision(key) != revision {
		t.Fatalf("artwork revision of %q = %q", key, Revision(key))
	}
	if path := StorageCoverPath("146532612416483348", "cover/book-1", "cover:abc"); !IsStorageCoverPath(path) || path != StorageCoverScheme+"://"+key {
		t.Fatalf("path = %q", path)
	}
	// A changed cover is a different key.
	if StorageCoverKey("146532612416483348", "cover/book-1", "cover:def") == key {
		t.Fatal("revision ignored")
	}
	// The length prefix keeps entry and revision boundaries apart.
	if StorageCoverRevision("ab", "c") == StorageCoverRevision("a", "bc") {
		t.Fatal("ambiguous revision hash")
	}
	for _, bad := range []string{"", "storage-covers/x", "storage-covers/x/w300." + revision + ".img", "storage-covers/x/original.short.img", "local/ebooks/x/original." + revision + ".img"} {
		if _, _, ok := ParseStorageCoverKey(bad); ok {
			t.Fatalf("parsed %q", bad)
		}
	}
	if StorageCoverKey("a/b", "e", "r") != "" || StorageCoverKey("a", "", "r") != "" {
		t.Fatal("invalid identity accepted")
	}
}
