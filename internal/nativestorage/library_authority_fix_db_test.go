//go:build integration

package nativestorage

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeDomainFullMigrationSnapshot(t *testing.T) {
	t.Helper()
	observed := map[string][32]byte{}
	combined := sha256.New()
	err := fs.WalkDir(migrations.FS, "sql", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		embedded, e := migrations.FS.ReadFile(path)
		if e != nil {
			return e
		}
		disk, e := os.ReadFile(filepath.Join("../../migrations", path))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(embedded)
		if sha256.Sum256(disk) != sum {
			return fmt.Errorf("full embedded/disk mismatch: %s", path)
		}
		observed[path] = sum
		fmt.Fprintf(combined, "%s:%x\n", path, sum)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("B FULL migrations BEFORE embedded=disk files=%d SHA256 %x", len(observed), combined.Sum(nil))
	t.Cleanup(func() {
		for path, sum := range observed {
			disk, e := os.ReadFile(filepath.Join("../../migrations", path))
			if e != nil || sha256.Sum256(disk) != sum {
				t.Errorf("full migration changed during gate: %s", path)
			}
		}
		entries, e := filepath.Glob("../../migrations/sql/*.sql")
		if e != nil || len(entries) != len(observed) {
			t.Error("full migration file inventory changed during gate")
		}
		t.Logf("B FULL migrations AFTER files=%d SHA256 %x", len(observed), combined.Sum(nil))
	})
}

// Test-only export for external actual-queue controls; public domain contracts
// remain frozen. Every fixture is constructed by the real legal commands.
func NativeDomainCrossOwnerFixtureForTest(t *testing.T, bound bool) (*pgxpool.Pool, auth.AdminContextClaims, auth.AdminContextClaims, SourceView, int) {
	t.Helper()
	pool := nativeDomainDatabase(t)
	platform, organization := nativeDomainActor(t, pool)
	_, source, _ := nativeDomainSourceFixture(t, pool, platform)
	libs := nativeDomainLibraries(pool)
	created, err := libs.Create(t.Context(), organization, LibraryCreateCommand{Name: "Cross owner authority books"})
	if err != nil {
		t.Fatal(err)
	}
	initialized, err := libs.Initialize(t.Context(), organization, created.LibraryID, created.LibraryRevision)
	if err != nil {
		t.Fatal(err)
	}
	if bound {
		if _, err = libs.Bind(t.Context(), platform, source.SourceKey, initialized.LibraryID, source.ConfigurationRevision, initialized.LibraryRevision); err != nil {
			t.Fatal(err)
		}
	}
	return pool, platform, organization, source, created.LibraryID
}
