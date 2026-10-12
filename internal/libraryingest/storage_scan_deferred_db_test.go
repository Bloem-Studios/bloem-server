//go:build integration

package libraryingest

import "testing"

// A provider's full listing is not a point-in-time snapshot. Bookwarehouse
// fixes an upper bound when a directory listing starts and leaves a book
// updated after it to the next listing, so a book the cursor had not reached
// when it was updated is missing from that full listing although it is still
// present. Such a book must stay published: only a later listing that still
// does not list it confirms it is gone.
func TestStorageScanKeepsBooksAFullListingDeferred(t *testing.T) {
	c := newCatalogScan(t)
	books := []catalogBook{{ID: "a", Title: "Alpha", Revision: "r1"}, {ID: "b", Title: "Beta", Revision: "r1"}, {ID: "c", Title: "Gamma", Revision: "r1"}}
	pdf := catalogDirectory{ID: "pdf", Files: []catalogBook{{ID: "p", Title: "Pi", Revision: "r1"}}}
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{{ID: "epub", Files: books}, pdf}})
	if got := c.mustRun(); got.New != 4 || got.Missing != 0 {
		t.Fatalf("first scan = %+v", got)
	}
	before := c.files()

	// The provider refuses the EPUB token and lists that directory in full.
	// Gamma was deleted before the listing; Beta is updated while it runs,
	// past the listing's upper bound, so the listing leaves it out as well.
	c.write(catalogSource{Token: "t1", Refuse: []string{"epub"}, Directories: []catalogDirectory{{ID: "epub", Files: books[:1]}, pdf}})
	if got := c.mustRun(); got.Missing != 0 {
		t.Fatalf("full listing that deferred a book = %+v", got)
	}
	for id, f := range c.files() {
		if f.Missing || !f.Member {
			t.Fatalf("%s left the library after one full listing: %+v", id, f)
		}
	}

	// The next listing, from the full listing's change token, lists Beta under
	// its new timestamp. Gamma is not listed and has no deletion record after
	// the token, so this listing confirms it is gone.
	updated := []catalogBook{books[0], {ID: "b", Title: "Beta, revised", Revision: "r2"}}
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{{ID: "epub", Files: updated, Changed: []string{"b"}}, pdf}})
	if got := c.mustRun(); got.Missing != 1 || got.Updated != 1 {
		t.Fatalf("incremental listing after the full listing = %+v", got)
	}
	after := c.files()
	if f := after["epub/c"]; !f.Missing || f.Member {
		t.Fatalf("deleted book kept: %+v", f)
	}
	for _, id := range []string{"epub/a", "epub/b", "pdf/p"} {
		if f := after[id]; f.Missing || !f.Member {
			t.Fatalf("present book %s left the library: %+v", id, f)
		}
	}
	// Beta never left the library, so it did not rejoin it as a new arrival.
	if b := after["epub/b"]; b.Title != "Beta, revised" || b.MemberXmin != before["epub/b"].MemberXmin {
		t.Fatalf("deferred book was re-added: before %+v after %+v", before["epub/b"], b)
	}
}
