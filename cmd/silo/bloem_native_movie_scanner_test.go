package main

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Startup assembly may not publish readiness for missing dependencies or an
// unconfigured scanner probe, including when a prior caller set the flag.
func TestNativeMovieScannerStartupRequiresCompleteDependencies(t *testing.T) {
	for _, missing := range []string{"deps", "database", "host", "registry", "runtime", "scanner", "catalog", "probe"} {
		t.Run(missing, func(t *testing.T) {
			pool := new(pgxpool.Pool)
			fileRepo := scanner.NewFileRepository(pool)
			deps := &api.Dependencies{DB: pool, NativeStorage: &nativestorage.Host{Registry: new(plugins.NativeStorageRegistry), Manager: storageplugin.NewManager(storageplugin.Config{})}, Scanner: scanner.NewScanner(fileRepo, "ffprobe", nil, 1, false, 0), FileRepo: fileRepo, BloemDependencies: api.BloemDependencies{NativeMovieScannerReady: true}}
			switch missing {
			case "deps":
				deps = nil
			case "database":
				deps.DB = nil
			case "host":
				deps.NativeStorage = nil
			case "registry":
				deps.NativeStorage.Registry = nil
			case "runtime":
				deps.NativeStorage.Manager = nil
			case "scanner":
				deps.Scanner = nil
			case "catalog":
				deps.FileRepo = nil
			case "probe":
				deps.Scanner = scanner.NewScanner(fileRepo, "", nil, 1, false, 0)
			}
			scanner, err := newNativeMovieStorageScanner(deps)
			if err == nil || scanner != nil {
				t.Fatalf("incomplete %s published scanner readiness", missing)
			}
			if deps != nil && deps.NativeMovieScannerReady {
				t.Fatalf("incomplete %s left readiness advertised", missing)
			}
		})
	}
}
