package scanner

import (
	"context"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

type nativeSiblingFixture struct {
	single  bool
	entries []*storagev1.Entry
}

func (f nativeSiblingFixture) SiblingCandidates(_ context.Context, _ storagesource.IngestionLease, dir string, names, suffixes []string) ([]*storagev1.Entry, bool, error) {
	if dir != "Books//../flat/" {
		panic("literal parent normalized")
	}
	hasZip := false
	for _, suffix := range suffixes {
		if suffix == ".zip" {
			hasZip = true
		}
	}
	if !hasZip {
		panic("unsupported fb2.zip excluded from generic-cover eligibility")
	}
	if len(names) > 128 || len(suffixes) > 128 {
		panic("unbounded candidates")
	}
	return f.entries, f.single, nil
}
func TestNativeSidecarResolverLiteralParentAndPrecedence(t *testing.T) {
	book := &storagev1.Entry{Id: "book", Name: "Book.epub", LogicalPath: "Books//../flat/Book.epub"}
	entries := []*storagev1.Entry{
		{Id: "generic", Name: "cover.jpg", LogicalPath: "Books//../flat/cover.jpg"},
		{Id: "png", Name: "BOOK.png", LogicalPath: "Books//../flat/BOOK.png"},
		{Id: "jpg", Name: "Book.jpg", LogicalPath: "Books//../flat/Book.jpg"},
		{Id: "opf", Name: "Book.opf", LogicalPath: "Books//../flat/Book.opf"},
	}
	got, err := ResolveNativeEbookSidecars(t.Context(), nativeSiblingFixture{true, entries}, storagesource.IngestionClaim{Entry: book}, nil)
	if err != nil || !got.Complete || got.OPF.Id != "opf" || got.Cover.Id != "jpg" {
		t.Fatalf("precedence lost: %+v %v", got, err)
	}
	got, err = ResolveNativeEbookSidecars(t.Context(), nativeSiblingFixture{false, entries[:1]}, storagesource.IngestionClaim{Entry: book}, nil)
	if err != nil || got.Cover != nil {
		t.Fatal("multi-book generic cover selected")
	}
	got, err = ResolveNativeEbookSidecars(t.Context(), nativeSiblingFixture{true, entries[:1]}, storagesource.IngestionClaim{Entry: book}, nil)
	if err != nil || got.Cover.Id != "generic" {
		t.Fatal("sole-book generic cover absent")
	}
}

type nativeRootSiblingFixture struct {
	parent string
	entry  *storagev1.Entry
}

func (f nativeRootSiblingFixture) SiblingCandidates(_ context.Context, _ storagesource.IngestionLease, parent string, names, suffixes []string) ([]*storagev1.Entry, bool, error) {
	if parent != f.parent || len(names) > 128 || len(suffixes) > 128 {
		return nil, false, storagesource.ErrCheckpointConflict
	}
	return []*storagev1.Entry{f.entry}, true, nil
}
func TestNativeSidecarResolverRootDelimiterIsolation(t *testing.T) {
	for _, prefix := range []string{"", "/", "//"} {
		t.Run("correct-"+prefix, func(t *testing.T) {
			book := &storagev1.Entry{Id: "book", Name: "Book.epub", LogicalPath: prefix + "Book.epub"}
			opf := &storagev1.Entry{Id: "opf", Name: "Book.opf", LogicalPath: prefix + "Book.opf"}
			got, err := ResolveNativeEbookSidecars(t.Context(), nativeRootSiblingFixture{prefix, opf}, storagesource.IngestionClaim{Entry: book}, nil)
			if err != nil || got.OPF != opf {
				t.Fatalf("literal root prefix %q lost: %v", prefix, err)
			}
		})
		for _, other := range []string{"", "/", "//"} {
			if other == prefix {
				continue
			}
			t.Run("wrong-"+prefix+"-"+other, func(t *testing.T) {
				book := &storagev1.Entry{Id: "book", Name: "Book.epub", LogicalPath: prefix + "Book.epub"}
				opf := &storagev1.Entry{Id: "opf", Name: "Book.opf", LogicalPath: other + "Book.opf"}
				got, err := ResolveNativeEbookSidecars(t.Context(), nativeRootSiblingFixture{prefix, opf}, storagesource.IngestionClaim{Entry: book}, nil)
				if err == nil || got.OPF != nil {
					t.Fatalf("wrong root sidecar %q selected for %q", other, prefix)
				}
			})
		}
	}
}
