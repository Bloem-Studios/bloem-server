package scanner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Test helpers Bloem's tests use, copied from Silo's tests before Silo pruned them
// (Silo #4465). Silo's test files no longer carry them.

type fakeEbookCoverCacher struct {
	calls     int
	data      []byte
	contentID string
	err       error
}

func (f *fakeEbookCoverCacher) CacheEbookCover(_ context.Context, data []byte, contentID string) (string, string, error) {
	f.calls++
	f.data = append([]byte(nil), data...)
	f.contentID = contentID
	if f.err != nil {
		return "", "", f.err
	}
	return "local/ebooks/" + contentID + "/poster/original.test-revision.webp", ebookCoverTestThumbhash, nil
}

func writeTestEPUBWithCover(t *testing.T, coverHref string, coverType string, coverBytes []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "book.epub")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	zw := zip.NewWriter(file)
	add := func(name string, body []byte) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	add("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))
	add("OPS/content.opf", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
  <metadata>
    <dc:title>The Test Ebook</dc:title>
    <meta name="cover" content="cover-image"/>
  </metadata>
  <manifest>
    <item id="cover-image" href="`+coverHref+`" media-type="`+coverType+`"/>
  </manifest>
</package>`))
	add("OPS/"+coverHref, coverBytes)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close epub: %v", err)
	}
	return path
}

const ebookCoverTestThumbhash = "thumb"
