//go:build integration

package libraryingest

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

// A complete full listing of a directory makes its former entries it omits
// suspect, and the next completed run that does not list them either confirms
// they are gone. Incremental, failed and suspiciously empty listings raise no
// suspicion, and an empty full listing confirms none.
func TestStorageScanInfersAbsenceFromCompleteFullListings(t *testing.T) {
	c := newCatalogScan(t)
	epub := catalogDirectory{ID: "epub", Files: []catalogBook{{ID: "a", Title: "Alpha", Revision: "r1"}, {ID: "b", Title: "Beta", Revision: "r1"}, {ID: "c", Title: "Gamma", Revision: "r1"}}}
	pdf := catalogDirectory{ID: "pdf", Files: []catalogBook{{ID: "p", Title: "Pi", Revision: "r1"}, {ID: "q", Title: "Rho", Revision: "r1"}}}
	without := func(d catalogDirectory, id string) catalogDirectory {
		d.Files = slices.DeleteFunc(slices.Clone(d.Files), func(b catalogBook) bool { return b.ID == id })
		return d
	}
	missing := func(want ...string) {
		t.Helper()
		var got []string
		for id, f := range c.files() {
			if f.Missing {
				got = append(got, id)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("missing files = %v, want %v", got, want)
		}
	}

	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub, pdf}})
	if got := c.mustRun(); got.New != 5 || got.Missing != 0 {
		t.Fatalf("first scan = %+v", got)
	}

	// Gamma is deleted, but the change feed does not report it: an incremental
	// listing never infers absence.
	epub = without(epub, "c")
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub, pdf}})
	if got := c.mustRun(); got.Missing != 0 {
		t.Fatalf("incremental scan = %+v", got)
	}
	missing()

	// The provider refuses the EPUB token and lists that directory in full,
	// but the PDF listing fails: the incomplete run proves nothing.
	c.write(catalogSource{Token: "t1", Refuse: []string{"epub"}, Fail: []string{"pdf"}, Directories: []catalogDirectory{epub, pdf}})
	if _, err := c.run(); err == nil {
		t.Fatal("failed listing completed")
	}
	failedRun, state := c.lastRun()
	if state != "running" {
		t.Fatalf("failed run state = %s", state)
	}
	missing()

	// The retry resumes that run; the PDF directory is listed incrementally.
	// The EPUB directory's complete full listing omits Gamma, which makes it
	// suspect but keeps it published: the provider may have left a book
	// updated during the listing to its next one.
	if _, err := c.pool.Exec(t.Context(), `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp() WHERE id=$1`, failedRun); err != nil {
		t.Fatal(err)
	}
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub, pdf}})
	got := c.mustRun()
	resumed, state := c.lastRun()
	if resumed != failedRun || state != "complete" || got.Missing != 0 || got.FilesDeleted != 0 {
		t.Fatalf("resumed scan = %+v run=%s (failed %s) state=%s", got, resumed, failedRun, state)
	}
	missing()

	// The next completed run does not list Gamma either: it is gone.
	if got := c.mustRun(); got.Missing != 1 {
		t.Fatalf("confirming scan = %+v", got)
	}
	missing("epub/c")
	if f := c.files()["epub/c"]; f.Member {
		t.Fatalf("absent book kept its library membership: %+v", f)
	}

	// Every directory listed in full: entries of a directory no longer listed
	// and entries journaled before directories were recorded are suspect too,
	// and the next run confirms them.
	epub = without(epub, "b")
	if _, err := c.pool.Exec(t.Context(), `UPDATE bloem_storage_entries SET directory_id=NULL WHERE source_key=$1 AND entry_id='epub/b'`, c.location.SourceKey); err != nil {
		t.Fatal(err)
	}
	c.write(catalogSource{Token: "t1", Refuse: []string{"epub"}, Directories: []catalogDirectory{epub}})
	if got := c.mustRun(); got.Missing != 0 {
		t.Fatalf("full scan = %+v", got)
	}
	missing("epub/c")
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub}})
	if got := c.mustRun(); got.Missing != 3 {
		t.Fatalf("confirming scan = %+v", got)
	}
	missing("epub/b", "epub/c", "pdf/p", "pdf/q")

	// Epsilon and Zeta arrive, then a full listing omits them and makes them
	// suspect. Zeta's journal entry has no directory, as entries journaled
	// before directories were recorded have none.
	epub.Files = append(epub.Files, catalogBook{ID: "e", Title: "Epsilon", Revision: "r1"}, catalogBook{ID: "z", Title: "Zeta", Revision: "r1"})
	epub.Changed = []string{"e", "z"}
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub}})
	if got := c.mustRun(); got.New != 2 {
		t.Fatalf("incremental scan = %+v", got)
	}
	epub.Changed = nil
	c.write(catalogSource{Token: "t1", Refuse: []string{"epub"}, Directories: []catalogDirectory{without(without(epub, "e"), "z")}})
	if got := c.mustRun(); got.Missing != 0 {
		t.Fatalf("full scan = %+v", got)
	}
	if _, err := c.pool.Exec(t.Context(), `UPDATE bloem_storage_entries SET directory_id=NULL WHERE source_key=$1 AND entry_id='epub/z'`, c.location.SourceKey); err != nil {
		t.Fatal(err)
	}

	// A full listing that comes back empty is suspect, as an empty filesystem
	// root is: it makes nothing suspect, and confirms neither Epsilon, in the
	// directory it listed, nor Zeta, whose directory is unknown.
	c.write(catalogSource{Token: "t1", Refuse: []string{"epub"}, Directories: []catalogDirectory{{ID: "epub"}}})
	if got := c.mustRun(); got.Missing != 0 {
		t.Fatalf("empty full listing = %+v", got)
	}
	missing("epub/b", "epub/c", "pdf/p", "pdf/q")
	if run, state := c.lastRun(); run == uuid.Nil || state != "complete" {
		t.Fatalf("empty listing run state = %s", state)
	}

	// A run that lists the directory incrementally confirms only the books
	// the full listing omitted.
	c.write(catalogSource{Token: "t1", Directories: []catalogDirectory{epub}})
	if got := c.mustRun(); got.Missing != 2 {
		t.Fatalf("confirming scan = %+v", got)
	}
	missing("epub/b", "epub/c", "epub/e", "epub/z", "pdf/p", "pdf/q")
}
