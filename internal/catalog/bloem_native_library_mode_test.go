package catalog

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeOnboardingRepositoryComposition(t *testing.T) {
	pool := new(pgxpool.Pool)
	folder := &FolderRepository{pool: pool}
	library := &LibraryItemRepository{pool: pool}
	episodeLibrary := &EpisodeLibraryRepository{pool: pool}
	item := &ItemRepository{pool: pool}
	person := &PersonRepository{pool: pool}
	episode := &EpisodeRepository{pool: pool}
	extra := &ExtraRepository{pool: pool}
	if !folder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, item, person, episode, extra) {
		t.Fatal("matching concrete repository pools must be ready")
	}
	if folder.NativeScannerRepositoriesReady(nil, library, episodeLibrary, item, person, episode, extra) {
		t.Fatal("nil pool")
	}
	for i := range 7 {
		f, l, el, it, p, e, x := *folder, *library, *episodeLibrary, *item, *person, *episode, *extra
		foreign := new(pgxpool.Pool)
		switch i {
		case 0:
			f.pool = foreign
		case 1:
			l.pool = foreign
		case 2:
			el.pool = foreign
		case 3:
			it.pool = foreign
		case 4:
			p.pool = foreign
		case 5:
			e.pool = foreign
		case 6:
			x.pool = foreign
		}
		if f.NativeScannerRepositoriesReady(pool, &l, &el, &it, &p, &e, &x) {
			t.Fatalf("foreign pool at repository %d", i)
		}
	}
	var noFolder *FolderRepository
	if noFolder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, item, person, episode, extra) {
		t.Fatal("nil folder")
	}
	if folder.NativeScannerRepositoriesReady(pool, nil, episodeLibrary, item, person, episode, extra) {
		t.Fatal("nil library")
	}
	if folder.NativeScannerRepositoriesReady(pool, library, nil, item, person, episode, extra) {
		t.Fatal("nil episode library")
	}
	if folder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, nil, person, episode, extra) {
		t.Fatal("nil item")
	}
	if folder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, item, nil, episode, extra) {
		t.Fatal("nil person")
	}
	if folder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, item, person, nil, extra) {
		t.Fatal("nil episode")
	}
	if folder.NativeScannerRepositoriesReady(pool, library, episodeLibrary, item, person, episode, nil) {
		t.Fatal("nil extra")
	}
}
