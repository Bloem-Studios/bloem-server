//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// overlapEntry is a directory whose logical path nests the way S3 keys do.
func overlapEntry(id, path string) *storagev1.Entry {
	return &storagev1.Entry{Id: id, Name: id, LogicalPath: path, Revision: "directory-v1", Kind: storagev1.EntryKind_ENTRY_KIND_DIRECTORY}
}

func TestLibraryLocationsRefuseOverlapAcrossLibrariesDB(t *testing.T) {
	b, a, s, provider := newBrowseFixture(t)
	provider.mu.Lock()
	provider.entries = map[string]*storagev1.Entry{
		"root":   overlapEntry("root", ""),
		"movies": overlapEntry("movies", "movies"),
		"kids":   overlapEntry("kids", "movies/kids"),
		"drama":  overlapEntry("drama", "movies/drama"),
		"audio":  overlapEntry("audio", "audio"),
		"moviex": overlapEntry("moviex", "movies-extra"),
	}
	provider.pages = map[string]*storagev1.ListResponse{
		"root|":   {Entries: []*storagev1.Entry{provider.entries["movies"], provider.entries["audio"], provider.entries["moviex"]}, Complete: true},
		"movies|": {Entries: []*storagev1.Entry{provider.entries["kids"], provider.entries["drama"]}, Complete: true},
	}
	provider.mu.Unlock()
	l := NewLibraryLocations(b.pool)
	l.SetSourceBrowser(b)

	root, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{"root": root.Current.SelectionRef}
	for _, d := range root.Directories {
		refs[d.Name] = d.SelectionRef
	}
	movies, err := b.Browse(t.Context(), a, s.Key, BrowseRequest{DirectoryRef: refs["movies"]})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range movies.Directories {
		refs[d.Name] = d.SelectionRef
	}
	pick := func(name string) []RootSelection {
		return []RootSelection{{SourceKey: s.Key, ConfigurationRevision: s.ConfigurationRevision, SelectionRef: refs[name]}}
	}
	create := func(name, folder string) (int, error) {
		p, err := l.PrepareCreation(t.Context(), a, nil, false, pick(folder))
		if err != nil {
			return 0, err
		}
		f, err := catalog.NewFolderRepository(l.pool).Create(t.Context(), catalog.CreateFolderInput{Name: name, Type: "ebooks", AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error { return l.AttachCreationTx(ctx, tx, a, id, p) }})
		if err != nil {
			return 0, err
		}
		return f.ID, nil
	}

	first, err := create("Movies", "movies")
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"movies", "kids", "root"} {
		_, err := create("Clash "+folder, folder)
		requireOverlap(t, err, folder)
	}
	// A sibling, even one whose name starts the same way, is separate.
	for _, folder := range []string{"audio", "moviex"} {
		if _, err := create("Separate "+folder, folder); err != nil {
			t.Fatalf("%s should not overlap: %v", folder, err)
		}
	}

	// Editing a library is held to the same rule; its own folder is not a clash.
	_, err = create("Spare", "audio")
	requireOverlap(t, err, "audio again")
	view, err := l.ReadSelections(t.Context(), a, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReplaceSelections(t.Context(), a, first, ReplaceLocationsRequest{Paths: []string{}, Selections: pick("movies"), ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken}); err != nil {
		t.Fatalf("keeping its own folder: %v", err)
	}
	view, err = l.ReadSelections(t.Context(), a, first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.ReplaceSelections(t.Context(), a, first, ReplaceLocationsRequest{Paths: []string{}, Selections: pick("root"), ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken})
	requireOverlap(t, err, "replace with root")

	// A folder the first library no longer selects is free again.
	view, err = l.ReadSelections(t.Context(), a, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReplaceSelections(t.Context(), a, first, ReplaceLocationsRequest{Paths: []string{}, Selections: pick("kids"), ExpectedRevision: view.Revision, ExpectedEditToken: view.EditToken}); err != nil {
		t.Fatalf("narrowing to kids: %v", err)
	}
	if _, err := create("Drama", "drama"); err != nil {
		t.Fatalf("drama was released: %v", err)
	}
	_, err = create("Kids", "kids")
	requireOverlap(t, err, "kids still held")
}

func requireOverlap(t *testing.T, err error, label string) {
	t.Helper()
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "selection_overlap" {
		t.Fatalf("%s: want selection_overlap, got %v", label, err)
	}
}
