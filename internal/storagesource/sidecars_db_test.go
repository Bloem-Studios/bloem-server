//go:build integration

package storagesource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func siblingFixture(t *testing.T, entries ...*storagev1.Entry) (*Repository, SourceConfig, IngestionLease, IngestionClaim) {
	t.Helper()
	pool := testDatabase(t, true)
	s, r := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91001)
	b, err := r.Bind(t.Context(), s.Key, 91001)
	if err != nil {
		t.Fatal(err)
	}
	l, err := r.Begin(t.Context(), s.Key, "discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, _, err := r.NextDirectory(t.Context(), l)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyPage(t.Context(), l, cp, &storagev1.ListResponse{Entries: entries, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	il, err := r.BeginIngestion(t.Context(), l.RunID, b.ID, "ingest", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := r.NextIngestion(t.Context(), il)
	if err != nil || !ok {
		t.Fatalf("ingestion claim: %v", err)
	}
	return r, s, il, c
}
func siblingEntry(id, name, logical string) *storagev1.Entry {
	return &storagev1.Entry{Id: id, Name: name, LogicalPath: logical, Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 12, Revision: "v1"}
}
func TestNativeSidecarCandidatesPinnedAndBounded(t *testing.T) {
	r, _, l, _ := siblingFixture(t,
		siblingEntry("a", "Book.epub", "dir/Book.epub"),
		siblingEntry("b", "Book.opf", "dir/Book.opf"),
		siblingEntry("c", "Book.jpg", "dir/Book.jpg"),
		siblingEntry("d", "cover.png", "dir/cover.png"),
		siblingEntry("e", "Book.jpg", "other/Book.jpg"))
	got, single, err := r.SiblingCandidates(t.Context(), l, "dir/", []string{"book.opf", "book.jpg", "cover.png"}, []string{".epub", ".pdf", ".mobi"})
	if err != nil || !single || len(got) != 3 {
		t.Fatalf("pinned sibling candidates: %d/%v %v", len(got), single, err)
	}
	for _, e := range got {
		if e.Id == "e" || e.Revision != "v1" {
			t.Fatal("wrong directory/revision")
		}
	}
}
func TestNativeSidecarCandidatesCountUnsupportedEbooks(t *testing.T) {
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"), siblingEntry("b", "Other.MOBI", "dir/Other.MOBI"))
	_, single, err := r.SiblingCandidates(t.Context(), l, "dir/", []string{"cover.jpg"}, []string{".epub", ".pdf", ".mobi"})
	if err != nil || single {
		t.Fatalf("generic cover falsely belongs to one book: %v", err)
	}
}
func TestNativeSidecarCandidatesRejectAmbiguityAndStaleLease(t *testing.T) {
	r, s, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"), siblingEntry("b", "Book.jpg", "dir/Book.jpg"), siblingEntry("c", "BOOK.JPG", "dir/BOOK.JPG"))
	if _, _, err := r.SiblingCandidates(t.Context(), l, "dir/", []string{"book.jpg"}, []string{".epub"}); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("ambiguous case collision selected: %v", err)
	}
	execSQL(t, r.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", s.Key)
	if _, _, err := r.SiblingCandidates(t.Context(), l, "dir/", nil, []string{".epub"}); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("stale source provided confirmed absence: %v", err)
	}
}
func TestNativeSidecarCandidatesPreserveLiteralDirectories(t *testing.T) {
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/../Book.epub"), siblingEntry("b", "Book.jpg", "Book.jpg"), siblingEntry("c", "Book.opf", "dir/../Book.opf"))
	got, single, err := r.SiblingCandidates(context.Background(), l, "dir/../", []string{"book.jpg", "book.opf"}, []string{".epub"})
	if err != nil || !single || len(got) != 1 || got[0].Id != "c" {
		t.Fatalf("opaque display directory normalized: %v", err)
	}
}

func TestNativeSidecarCandidatesKeepDoubleSlashesAndRoot(t *testing.T) {
	r, _, l, _ := siblingFixture(t,
		siblingEntry("a", "Book.epub", "dir//Book.epub"),
		siblingEntry("b", "Book.jpg", "dir//Book.jpg"),
		siblingEntry("c", "Book.jpg", "dir/Book.jpg"),
		siblingEntry("d", "Book.opf", "Book.opf"))
	got, single, err := r.SiblingCandidates(t.Context(), l, "dir//", []string{"book.jpg"}, []string{".epub"})
	if err != nil || !single || len(got) != 1 || got[0].Id != "b" {
		t.Fatalf("double slash changed: %v/%v %v", got, single, err)
	}
	got, single, err = r.SiblingCandidates(t.Context(), l, "", []string{"book.opf"}, []string{".epub"})
	if err != nil || single || len(got) != 1 || got[0].Id != "d" {
		t.Fatalf("root sibling changed: %v/%v %v", got, single, err)
	}
}

func TestNativeSidecarCandidatesMaximumLiteralLengths(t *testing.T) {
	name := strings.Repeat("x", 4092) + ".jpg"
	directory := strings.Repeat("d", 65536-len(name)-1)
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", directory+"/Book.epub"), siblingEntry("b", name, directory+"/"+name))
	got, single, err := r.SiblingCandidates(t.Context(), l, directory+"/", []string{name}, []string{".epub"})
	if err != nil || !single || len(got) != 1 || got[0].Id != "b" {
		t.Fatalf("maximum lengths rejected: %v/%v %v", len(got), single, err)
	}
}

func TestNativeSidecarCandidatesRejectOversizedAndDuplicateRequests(t *testing.T) {
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"))
	for _, names := range [][]string{make([]string, 129), {"Book.jpg", "BOOK.JPG"}, {strings.Repeat("x", 4097)}} {
		if _, _, err := r.SiblingCandidates(t.Context(), l, "dir/", names, []string{".epub"}); !errors.Is(err, ErrCheckpointConflict) {
			t.Fatalf("invalid candidate request accepted: %v", err)
		}
	}
}

func TestNativeSidecarCandidatesAlwaysCountMOBI(t *testing.T) {
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"), siblingEntry("b", "Other.MOBI", "dir/Other.MOBI"))
	_, single, err := r.SiblingCandidates(t.Context(), l, "dir/", nil, []string{".epub", ".pdf"})
	if err != nil || single {
		t.Fatalf("unsupported MOBI omitted from cover eligibility: %v", err)
	}
}

func TestNativeSidecarCandidatesReturnAtMostRequestedNames(t *testing.T) {
	entries := []*storagev1.Entry{siblingEntry("book", "Book.epub", "dir/Book.epub")}
	names := make([]string, 128)
	for i := range names {
		name := fmt.Sprintf("image-%03d.jpg", i)
		names[i] = name
		entries = append(entries, siblingEntry(fmt.Sprintf("image-%03d", i), name, "dir/"+name))
	}
	entries = append(entries, siblingEntry("unrequested", "other.jpg", "dir/other.jpg"))
	r, _, l, _ := siblingFixture(t, entries...)
	got, single, err := r.SiblingCandidates(t.Context(), l, "dir/", names, []string{".epub"})
	if err != nil || !single || len(got) != 128 {
		t.Fatalf("candidate bound: %d/%v %v", len(got), single, err)
	}
	for _, entry := range got {
		if entry.Id == "unrequested" {
			t.Fatal("unrequested sibling returned")
		}
	}
}

func TestNativeSidecarCandidatesFenceInvalidGenerations(t *testing.T) {
	for _, scenario := range []string{"disabled", "expired", "superseded", "running", "binding-removed"} {
		t.Run(scenario, func(t *testing.T) {
			r, s, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"))
			want := ErrStaleLease
			switch scenario {
			case "disabled":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", s.Key)
			case "expired":
				execSQL(t, r.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE run_id=$1", l.RunID)
			case "superseded":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET discovery_run_id=NULL WHERE key=$1", s.Key)
			case "running":
				execSQL(t, r.pool, "UPDATE bloem_storage_scan_runs SET state='running' WHERE id=$1", l.RunID)
			case "binding-removed":
				execSQL(t, r.pool, "DELETE FROM bloem_storage_bindings WHERE id=$1", l.BindingID)
				want = ErrReferenceConflict
			}
			if _, _, err := r.SiblingCandidates(t.Context(), l, "dir/", nil, []string{".epub"}); !errors.Is(err, want) {
				t.Fatalf("unfenced absence: %v", err)
			}
		})
	}
}

func TestNativeSidecarLookupMigrationRoundTrip(t *testing.T) {
	r, _, l, _ := siblingFixture(t, siblingEntry("a", "Book.epub", "dir/Book.epub"), siblingEntry("b", "Book.jpg", "dir/Book.jpg"))
	execSQL(t, r.pool, sidecarMigrationSQL(t, false))
	execSQL(t, r.pool, sidecarMigrationSQL(t, true))
	got, single, err := r.SiblingCandidates(t.Context(), l, "dir/", []string{"book.jpg"}, []string{".epub"})
	if err != nil || !single || len(got) != 1 || got[0].Id != "b" {
		t.Fatalf("migration changed retained entries: %v/%v %v", len(got), single, err)
	}
}
