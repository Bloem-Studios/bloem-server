package scanner

import (
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func TestIsPlaceholderISBN(t *testing.T) {
	for isbn, want := range map[string]bool{
		"":              false,
		"0000000000000": true,
		"2222222222222": true,
		"0000000000":    true,
		"1111111111":    true,
		"5000000000005": true, // valid checksum, outside the 978/979 book prefixes
		"9780441013593": false,
		"9791034304000": false,
		"0441013597":    false,
	} {
		if got := isPlaceholderISBN(isbn); got != want {
			t.Errorf("isPlaceholderISBN(%q) = %v, want %v", isbn, got, want)
		}
	}
}

func TestStorageEbookDropsPlaceholderISBN(t *testing.T) {
	entry := func(id string, meta *storagev1.EbookMetadata) StorageEbook {
		t.Helper()
		book, ok := StorageEbookFromEntry(storageEbookEntry(id, "epub", meta, "r1"))
		if !ok {
			t.Fatal("entry carries no metadata")
		}
		return book
	}

	dune := entry("dune", &storagev1.EbookMetadata{Title: "Dune", Authors: []string{"Frank Herbert"}, Isbn: "978-0-441-01359-3"})
	if dune.Book.ISBN != "9780441013593" || dune.isbnPlaceholder {
		t.Fatalf("real ISBN = %q placeholder=%v", dune.Book.ISBN, dune.isbnPlaceholder)
	}
	if key := storageEbookGroupKey(&dune); key != "ebook:isbn:9780441013593" {
		t.Fatalf("real ISBN key = %q", key)
	}

	// A placeholder ISBN is dropped; the book is grouped by title and author.
	authored := entry("alpha", &storagev1.EbookMetadata{Title: "Alpha", Authors: []string{"Ann Author"}, Isbn: "0000000000000"})
	if authored.Book.ISBN != "" || !authored.isbnPlaceholder {
		t.Fatalf("placeholder ISBN kept: %q placeholder=%v", authored.Book.ISBN, authored.isbnPlaceholder)
	}
	if key := storageEbookGroupKey(&authored); key != "ebook:title_author:alpha|ann author" {
		t.Fatalf("authored key = %q", key)
	}

	// Without an author, the book is its own path: two such books sharing a
	// title stay apart.
	first := entry("one", &storagev1.EbookMetadata{Title: "Notes", Isbn: "0000000000000"})
	second := entry("two", &storagev1.EbookMetadata{Title: "Notes", Isbn: "0000000000000"})
	if a, b := storageEbookGroupKey(&first), storageEbookGroupKey(&second); a != "ebook:path:epub/one.epub" || b != "ebook:path:epub/two.epub" {
		t.Fatalf("authorless keys = %q, %q", a, b)
	}

	// A book with no ISBN at all keeps the ordinary title grouping.
	plain := entry("plain", &storagev1.EbookMetadata{Title: "Notes"})
	if key := storageEbookGroupKey(&plain); key != "ebook:title:notes|dir:epub" {
		t.Fatalf("no-ISBN key = %q", key)
	}

	if !placeholderISBNGroupKey("ebook:isbn:0000000000000") || placeholderISBNGroupKey("ebook:isbn:9780441013593") || placeholderISBNGroupKey("ebook:title:notes|dir:epub") {
		t.Fatal("placeholderISBNGroupKey misclassifies keys")
	}
}
