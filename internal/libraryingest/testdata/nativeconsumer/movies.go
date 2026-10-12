package main

import (
	"os"
	"strings"

	"google.golang.org/protobuf/proto"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// The test owns both fixture files. Their paths are runtime configuration,
// never discovered entries or a production provider's addressing mechanism.
func movieFixtures(prefix, revision string) ([]object, error) {
	var result []object
	for _, f := range []struct{ id, name, suffix string }{
		{"01-movie", "Native.Movie.2024.mp4", ".mp4"},
		{"02-movie", "Native.Second.2025.mkv", ".mkv"},
	} {
		data, err := os.ReadFile(prefix + f.suffix)
		if err != nil {
			return nil, err
		}
		result = append(result, object{&storagev1.Entry{Id: f.id, Name: f.name, LogicalPath: "Movies//../" + f.name,
			Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: int64(len(data)), Revision: revision, ModifiedUnixNano: 1700000000000000000}, data})
	}
	data := []byte("https://untrusted.invalid/remote-movie")
	result = append(result, object{&storagev1.Entry{Id: "03-shortcut", Name: "remote.strm", LogicalPath: "Movies/remote.strm",
		Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: int64(len(data)), Revision: revision, ModifiedUnixNano: 1700000000000000000}, data})
	return result, nil
}

// movieFolderFixtures lists title folders named like the live movies bucket,
// flattened into the root listing with their sidecars, an extra and two
// directories the library never lists. Listing either directory fails: this
// fixture serves the root only.
func movieFolderFixtures(prefix, revision string) ([]object, error) {
	mp4, err := os.ReadFile(prefix + ".mp4")
	if err != nil {
		return nil, err
	}
	mkv, err := os.ReadFile(prefix + ".mkv")
	if err != nil {
		return nil, err
	}
	const threeAM = "3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}/"
	const heat = "Heat (1995) {tmdb-949}/"
	var result []object
	for _, f := range []struct {
		id, logical string
		data        []byte
		directory   bool
	}{
		{"01-recycle", ".recyclebin", nil, true},
		{"02-three-am", threeAM + "3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}.mp4", mp4, false},
		{"03-three-am-srt", threeAM + "3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}.en.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), false},
		{"04-three-am-poster", threeAM + "poster.jpg", []byte("not a jpeg"), false},
		{"05-eerie", "13 Eerie (2013)/xyz.mkv", mkv, false},
		{"06-heat-trailers", heat + "Trailers", nil, true},
		{"07-heat-trailer", heat + "Trailers/trailer.mp4", mp4, false},
		{"08-heat-cut", heat + "Heat (1995) - Director's Cut.mp4", mp4, false},
		{"09-heat", heat + "Heat (1995).mkv", mkv, false},
		{"10-heat-sample", heat + "Sample/heat.mp4", mp4, false},
	} {
		name := f.logical[strings.LastIndex(f.logical, "/")+1:]
		entry := &storagev1.Entry{Id: f.id, Name: name, LogicalPath: f.logical, Kind: storagev1.EntryKind_ENTRY_KIND_FILE,
			Size: int64(len(f.data)), Revision: revision, ModifiedUnixNano: 1700000000000000000}
		if f.directory {
			entry.Kind, entry.Revision = storagev1.EntryKind_ENTRY_KIND_DIRECTORY, "directory"
		}
		result = append(result, object{entry, f.data})
	}
	return result, nil
}

// The marker changes a previously ordinary movie listing into one complete
// page of the same entry IDs under directories the scanner excludes.
func (s *server) filteredMovieFolderListing() []object {
	if _, err := os.Stat(s.notify + ".excluded"); err != nil {
		return s.objects
	}
	var files []object
	for _, file := range s.objects {
		var directory string
		switch file.entry.GetId() {
		case "02-three-am":
			directory = "Sample"
		case "05-eerie":
			directory = "Trailers"
		default:
			continue
		}
		entry := proto.CloneOf(file.entry)
		slash := strings.LastIndex(entry.LogicalPath, "/")
		entry.LogicalPath = entry.LogicalPath[:slash+1] + directory + "/" + entry.Name
		files = append(files, object{entry: entry, data: file.data})
	}
	return files
}
