package main

import (
	"os"

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
