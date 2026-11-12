package storagesource

import (
	"reflect"
	"testing"
)

// Jellyfin clients cannot play native-storage files yet, so the compatibility
// server hides every library that reads from a storage source. A library
// with paths only stays visible; a mixed library counts as storage backed.
func TestLibrariesByStorageSplitsStorageBackedLibrariesDB(t *testing.T) {
	pool := storageTestPool(t)
	source, repo := fixtureSource(t, pool)
	for _, id := range []int{91001, 91002, 91003} {
		fixtureFolder(t, pool, id)
	}
	without, with, err := repo.LibrariesByStorage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(with) != 0 || !containsAll(without, 91001, 91002, 91003) {
		t.Fatalf("before any location: without=%v with=%v", without, with)
	}
	fixtureLocation(t, repo, source.Key, 91002)
	other, err := repo.CreateSource(t.Context(), SourceConfig{PluginID: "fixture", ProviderSourceID: "other", RootEntryID: "other-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	third, err := repo.CreateSource(t.Context(), SourceConfig{PluginID: "fixture", ProviderSourceID: "third", RootEntryID: "third-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixtureLocation(t, repo, other.Key, 91003)
	fixtureLocation(t, repo, third.Key, 91003)
	without, with, err = repo.LibrariesByStorage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(with, []int{91002, 91003}) {
		t.Fatalf("storage-backed libraries = %v, want [91002 91003] once each", with)
	}
	if !containsAll(without, 91001) || containsAny(without, 91002, 91003) {
		t.Fatalf("libraries without storage = %v", without)
	}
}

func containsAll(values []int, want ...int) bool {
	for _, w := range want {
		if !containsAny(values, w) {
			return false
		}
	}
	return true
}

func containsAny(values []int, want ...int) bool {
	for _, v := range values {
		for _, w := range want {
			if v == w {
				return true
			}
		}
	}
	return false
}
