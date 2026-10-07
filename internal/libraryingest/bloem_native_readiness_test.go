package libraryingest

import (
	"bytes"
	"testing"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeStorageExecutorComposition(t *testing.T) {
	// Only constructor wiring is observed here. No pool connection or schema
	// readiness is claimed; the authenticated integration gate covers those.
	pool, foreign := &pgxpool.Pool{}, &pgxpool.Pool{}
	cipher, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{})}
	folders := catalog.NewFolderRepository(pool)
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
	consumer, err := NewNativeConsumer(host, storagesource.NewRepository(pool), resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(publisher, nil, folders, nil, nil, nil)
	executor.SetNativeIngestor(consumer)
	if executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("publisher without cover cacher admitted")
	}
	var absent *imagecache.Cacher
	publisher.SetImageCacher(absent)
	if executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("typed-nil cover cacher admitted")
	}
	assets, err := blobstore.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cacher := imagecache.New(assets)
	cacher.SetArtworkRevisionTracker(catalog.NewArtworkRevisionTracker(pool))
	publisher.SetImageCacher(cacher)
	executor.SetNativeIngestor(nil)
	if executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("missing native consumer admitted")
	}
	executor.SetNativeIngestor(consumer)
	if !executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("actual constructor composition missing")
	}
	for name, mismatch := range map[string]func() bool{
		"pool": func() bool { return executor.NativeStorageComposition(foreign, host, folders, publisher) },
		"folder instance": func() bool {
			return executor.NativeStorageComposition(pool, host, catalog.NewFolderRepository(pool), publisher)
		},
		"publisher instance": func() bool {
			return executor.NativeStorageComposition(pool, host, folders, scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0))
		},
		"host instance": func() bool {
			return executor.NativeStorageComposition(pool, &nativestorage.Host{Registry: registry, Manager: host.Manager}, folders, publisher)
		},
	} {
		if mismatch() {
			t.Fatalf("mismatched %s admitted", name)
		}
	}
	wrong, err := NewNativeConsumer(host, storagesource.NewRepository(foreign), resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetNativeIngestor(wrong)
	if executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("foreign source store admitted")
	}
	executor.SetNativeIngestor(consumer)
	if err := host.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if executor.NativeStorageComposition(pool, host, folders, publisher) {
		t.Fatal("closed host admitted")
	}
}
