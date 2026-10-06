package scanner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

func nativeIngestInput(t *testing.T) (storagesource.IngestionClaim, *nativeTestFile) {
	t.Helper()
	data, err := os.ReadFile(writeTestEPUB(t, []string{"ISBN: 978-0-306-40615-7"}))
	if err != nil {
		t.Fatal(err)
	}
	f := nativeBytes("book.epub", data)
	f.info.Revision = "v1"
	f.info.LogicalPath = "Books/book.epub"
	c := storagesource.IngestionClaim{Lease: storagesource.IngestionLease{BindingID: uuid.New()}, Token: uuid.New(), Entry: &storagev1.Entry{Id: "book", Name: f.info.Name, LogicalPath: f.info.LogicalPath, Revision: f.info.Revision, Size: f.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}}
	return c, f
}

func TestNativeIngestRequiresExplicitSidecarDiscovery(t *testing.T) {
	c, f := nativeIngestInput(t)
	if _, _, err := parseNativeIngest(context.Background(), c, f, NativeEbookSidecars{}); !errors.Is(err, ErrNativeEbookSidecarsIncomplete) {
		t.Fatalf("unknown discovery published: %v", err)
	}
	book, _, err := parseNativeIngest(context.Background(), c, f, NativeEbookSidecars{Complete: true})
	if err != nil || book.Title != "The Test Ebook" || book.ISBN != "9780306406157" || len(book.Authors) == 0 {
		t.Fatalf("actual metadata lost: %+v %v", book, err)
	}
}

func TestNativeIngestSidecarPrecedenceAndOutage(t *testing.T) {
	c, f := nativeIngestInput(t)
	opf := []byte(`<package><metadata><title>Sidecar Title</title><creator>Sidecar Author</creator><language>nl</language><meta name="calibre:series" content="Sidecar Series"/><meta name="calibre:series_index" content="2"/></metadata></package>`) //nolint:misspell // Calibre XML metadata keys require this exact protocol spelling.
	entry := &storagev1.Entry{Id: "opf", Name: "book.opf", LogicalPath: "Books/book.opf", Revision: "opf-v1", Size: int64(len(opf)), Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	input := NativeEbookSidecars{Complete: true, OPF: entry, Open: func(_ context.Context, ref storagesource.PersistedRef) (mediasource.File, error) {
		if ref.BindingID != c.Lease.BindingID || ref.EntryID != entry.Id || ref.Revision != entry.Revision {
			t.Fatal("unpinned sidecar ref")
		}
		sf := nativeBytes(entry.Name, opf)
		sf.info.Revision = entry.Revision
		sf.info.LogicalPath = entry.LogicalPath
		return sf, nil
	}}
	book, refs, err := parseNativeIngest(context.Background(), c, f, input)
	if err != nil || book.Title != "Sidecar Title" || book.Authors[0] != "Sidecar Author" || book.Language != "nl" || book.Series != "Sidecar Series" || len(refs) != 1 {
		t.Fatalf("sidecar metadata %+v refs=%d %v", book, len(refs), err)
	}
	outage := errors.New("sidecar unavailable")
	input.Open = func(context.Context, storagesource.PersistedRef) (mediasource.File, error) { return nil, outage }
	if _, _, err = parseNativeIngest(context.Background(), c, f, input); !errors.Is(err, outage) {
		t.Fatalf("outage skipped: %v", err)
	}
}

func TestNativeIngestPinnedBoundsAndNoLocalProbe(t *testing.T) {
	c, f := nativeIngestInput(t)
	// A real malformed sidecar at the logical path must never be read.
	logical := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(filepath.Join(filepath.Dir(logical), "book.opf"), []byte("bad xml"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Entry.LogicalPath = logical
	f.info.LogicalPath = logical
	if _, _, err := parseNativeIngest(context.Background(), c, f, NativeEbookSidecars{Complete: true}); err != nil {
		t.Fatal(err)
	}
	f.info.Revision = "v2"
	if _, _, err := parseNativeIngest(context.Background(), c, f, NativeEbookSidecars{Complete: true}); err == nil {
		t.Fatal("wrong pinned revision accepted")
	}
	f.info.Revision = "v1"
	called := false
	input := NativeEbookSidecars{Complete: true, OPF: &storagev1.Entry{Id: "opf", Name: "book.opf", LogicalPath: filepath.Join(filepath.Dir(logical), "book.opf"), Revision: "v1", Size: maxEPUBMetadataEntrySize + 1, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}, Open: func(context.Context, storagesource.PersistedRef) (mediasource.File, error) {
		called = true
		return nil, nil
	}}
	if _, _, err := parseNativeIngest(context.Background(), c, f, input); err == nil || called {
		t.Fatal("oversize sidecar opened")
	}
}

func TestNativeIngestSidecarCoverPrecedence(t *testing.T) {
	c, f := nativeIngestInput(t)
	data, err := os.ReadFile(writeTestEPUBWithCover(t, "embedded.jpg", "image/jpeg", []byte("embedded-cover")))
	if err != nil {
		t.Fatal(err)
	}
	f.Reader = bytes.NewReader(data)
	f.info.Size = int64(len(data))
	c.Entry.Size = f.info.Size
	sidecar := []byte("selected-sidecar-cover")
	e := &storagev1.Entry{Id: "cover", Name: "book.jpg", LogicalPath: "Books/book.jpg", Revision: "cover-v1", Size: int64(len(sidecar)), Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	input := NativeEbookSidecars{Complete: true, Cover: e, Open: func(context.Context, storagesource.PersistedRef) (mediasource.File, error) {
		sf := nativeBytes(e.Name, sidecar)
		sf.info.Revision = e.Revision
		sf.info.LogicalPath = e.LogicalPath
		return sf, nil
	}}
	book, _, err := parseNativeIngest(context.Background(), c, f, input)
	if err != nil || book.Cover == nil || !bytes.Equal(book.Cover.Bytes, sidecar) {
		t.Fatalf("sidecar precedence lost %+v %v", book.Cover, err)
	}
	e.Name = "cover.jpg"
	e.LogicalPath = "Books/cover.jpg"
	if _, _, err = parseNativeIngest(context.Background(), c, f, input); err == nil {
		t.Fatal("generic cover in unconfirmed multibook directory accepted")
	}
	input.SingleEbookInDirectory = true
	if _, _, err = parseNativeIngest(context.Background(), c, f, input); err != nil {
		t.Fatal(err)
	}
}

// A cleaned path comparison admits sidecars from a different literal S3 prefix.
func TestNativeIngestSidecarsPreserveLiteralParent(t *testing.T) {
	for _, tc := range []struct {
		name, bookPath, sidecarPath string
		want                        bool
	}{
		{"double-slash-same", "Books//book.epub", "Books//book.opf", true},
		{"double-slash-distinct", "Books//book.epub", "Books/book.opf", false},
		{"dotdot-same", "Books/../Shelf/book.epub", "Books/../Shelf/book.opf", true},
		{"dotdot-distinct", "Books/../Shelf/book.epub", "Shelf/book.opf", false},
		{"dot-distinct", "Books/./book.epub", "Books/book.opf", false},
		{"root-same", "book.epub", "book.opf", true},
		{"root-slash-distinct", "/book.epub", "book.opf", false},
		{"literal-name", "Books/book.epub", "Books/other.opf", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			book := &storagev1.Entry{Name: "book.epub", LogicalPath: tc.bookPath}
			e := &storagev1.Entry{Name: "book.opf", LogicalPath: tc.sidecarPath}
			if got := nativeEbookSidecarEligible(book, e, true, false); got != tc.want {
				t.Fatalf("literal sidecar parent eligibility=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestNativeIngestAuthorizedRequiresCallbackBeforePreparation(t *testing.T) {
	var s *Scanner
	if _, err := s.PublishAuthorizedNativeEbook(context.Background(), nil, storagesource.IngestionClaim{}, nil, nil, NativeEbookSidecars{}, nil); !errors.Is(err, storagesource.ErrIngestionAuthorizationRequired) {
		t.Fatalf("nil authorization did not fail closed before preparation: %v", err)
	}
}

func TestNativeIngestSemanticGroupKeysKeepBindingScope(t *testing.T) {
	binding := uuid.MustParse("dd3b049e-44c4-4cf0-b99d-aad79524ce8c")
	book := &parsedEbook{Title: "The Same Book", Authors: []string{"Ada Writer", "Ben Author"}}
	key := nativeEbookContentGroupKey(binding, book, "Books//book.epub")
	if key != "bloem-native:dd3b049e-44c4-4cf0-b99d-aad79524ce8c:ebook:title_author:the same book|ada writer,ben author" {
		t.Fatalf("title/author semantic identity lost: %s", key)
	}
	if other := nativeEbookContentGroupKey(binding, book, "Other/../Shelf/book.pdf"); other != key {
		t.Fatalf("semantic multi-format group became directory-scoped: %s / %s", key, other)
	}
	if other := nativeEbookContentGroupKey(uuid.MustParse("89929fce-a2b2-4bc0-ad76-7816b7793655"), book, "Books//book.pdf"); other == key {
		t.Fatal("binding scope missing from semantic group")
	}
	book.Title = "Unrelated title"
	if other := nativeEbookContentGroupKey(binding, book, "Books//book.pdf"); other == key {
		t.Fatal("different title/author semantic keys merged")
	}
}
