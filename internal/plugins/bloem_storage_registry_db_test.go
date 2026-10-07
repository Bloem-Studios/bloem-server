//go:build integration

package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func nativeRegistryDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	template := config.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-disposable database")
	}
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	name := "bloem_storage_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	var locations *string
	if err = pool.QueryRow(t.Context(), `SELECT to_regclass('library_storage_locations')::text`).Scan(&locations); err != nil {
		t.Fatal(err)
	}
	if locations == nil {
		t.Fatal("test template is not migrated to the current schema")
	}
	return pool
}
func nativeRegistryFixture(t *testing.T) (*NativeStorageRegistry, *NativeStorageSnapshot, NativeStorageInstallRequest) {
	t.Helper()
	pool := nativeRegistryDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	a := approvedNativeFixture()
	sum := sha256.Sum256(binary)
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum = a.Checksum
	registry, err := NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]NativeStorageArtifact{"fixture": a})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	req := NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary, Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true}, Config: map[string]map[string]any{"source": {"secret": "synthetic-only-secret", "bucket": "fixture"}}}
	installed, err := registry.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return registry, installed, req
}
func TestNativeStorageRegistryAtomicInstallSnapshot(t *testing.T) {
	r, installed, req := nativeRegistryFixture(t)
	s, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Generation != 1 || s.Source.ConfigurationRevision != 1 || len(s.Config) != 1 || s.Config[0].Value.AsMap()["secret"] != "synthetic-only-secret" {
		t.Fatalf("invalid snapshot %+v", s)
	}
	var raw string
	var caps int
	if err = r.pool.QueryRow(t.Context(), `SELECT config_value::text FROM plugin_runtime_configs WHERE plugin_installation_id=$1`, s.Installation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "synthetic-only-secret") || !strings.Contains(raw, encryptedRuntimeConfigField) {
		t.Fatal("native config is not encrypted")
	}
	if err = r.pool.QueryRow(t.Context(), `SELECT count(*) FROM plugin_capabilities WHERE plugin_installation_id=$1`, s.Installation.ID).Scan(&caps); err != nil || caps != 0 {
		t.Fatalf("public capability leak: %d %v", caps, err)
	}
	if _, err = r.Snapshot(t.Context(), s.Source.Key, uuid.New()); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("wrong owner: %v", err)
	}
	// Fresh repository instance reads durable config and a detached snapshot cannot mutate approval.
	s.Manifest.PluginId = "tampered"
	s.Config[0].Value.Fields = nil
	second := &NativeStorageRegistry{pool: r.pool, configs: r.configs, baseDir: r.baseDir, approved: r.approved}
	if _, err = second.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err != nil {
		t.Fatal(err)
	}
	before := 0
	if err = r.pool.QueryRow(t.Context(), `SELECT count(*) FROM plugin_installations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	req.ArtifactKey = "request-cannot-self-approve"
	if _, err = r.Install(t.Context(), req); err == nil {
		t.Fatal("unapproved binary accepted")
	}
	req.ArtifactKey = "fixture"
	req.Config = map[string]map[string]any{"bad": {"invalid": make(chan int)}}
	if _, err = r.Install(t.Context(), req); err == nil {
		t.Fatal("invalid config accepted")
	}
	var after int
	if err = r.pool.QueryRow(t.Context(), `SELECT count(*) FROM plugin_installations`).Scan(&after); err != nil || after != before {
		t.Fatalf("partial installation: %d %d %v", before, after, err)
	}
	dirs, err := os.ReadDir(r.baseDir)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("rollback leaked package: %d %v", len(dirs), err)
	}
	if _, err = r.pool.Exec(t.Context(), `UPDATE plugin_archives SET archive_bytes='tampered'::bytea WHERE plugin_installation_id=$1`, installed.Installation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
		t.Fatal("tampered archive accepted")
	}
}
func TestNativeStorageRegistryConcurrentConfigurationFence(t *testing.T) {
	r, s, _ := nativeRegistryFixture(t)
	repo := storagesource.NewRepository(r.pool)
	lease, err := repo.Begin(t.Context(), s.Source.Key, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 4
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			_, err := r.ReplaceConfiguration(ctx, s.Source.Key, s.Source.OwnerID, map[string]map[string]any{"source": {"bucket": "replacement"}})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Snapshot(t.Context(), s.Source.Key, s.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Generation != 1+writers || got.Source.ConfigurationRevision != 1+writers {
		t.Fatalf("lost revision: %d %d", got.Generation, got.Source.ConfigurationRevision)
	}
	if err = repo.Renew(t.Context(), lease, time.Minute); !errors.Is(err, storagesource.ErrStaleLease) {
		t.Fatalf("old scan not fenced: %v", err)
	}
}
func TestNativeStorageRegistryDisableUninstallRetainsSource(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "disable", true: "uninstall"}[uninstall], func(t *testing.T) {
			r, s, _ := nativeRegistryFixture(t)
			repo := storagesource.NewRepository(r.pool)
			lease, err := repo.Begin(t.Context(), s.Source.Key, "worker", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if uninstall {
				err = r.Uninstall(t.Context(), s.Installation.ID, s.Source.OwnerID)
			} else {
				err = r.Disable(t.Context(), s.Installation.ID, s.Source.OwnerID)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Source(t.Context(), s.Source.Key)
			if err != nil || got.OwnerID != s.Source.OwnerID || got.InstallationID != nil || got.Enabled {
				t.Fatalf("retained source: %+v %v", got, err)
			}
			if err = repo.Renew(t.Context(), lease, time.Minute); !errors.Is(err, storagesource.ErrStaleLease) {
				t.Fatalf("old scan not fenced: %v", err)
			}
			if _, err = r.Snapshot(t.Context(), s.Source.Key, s.Source.OwnerID); !errors.Is(err, storagesource.ErrSourceUnavailable) {
				t.Fatalf("removed source runnable: %v", err)
			}
		})
	}
}

func TestNativeStorageRegistryReinstallRetainsResource(t *testing.T) {
	r, s, req := nativeRegistryFixture(t)
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO media_folders(id,type,name) VALUES(91919,'ebooks','synthetic fixture')`); err != nil {
		t.Fatal(err)
	}
	location := uuid.New()
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,91919)`, location, s.Source.Key); err != nil {
		t.Fatal(err)
	}
	if err := r.Uninstall(t.Context(), s.Installation.ID, s.Source.OwnerID); err != nil {
		t.Fatal(err)
	}
	req.Source = s.Source
	req.Source.InstallationID = nil
	replacement, err := r.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Source.Key != s.Source.Key || replacement.Source.ConfigurationRevision != 3 || replacement.Installation.ID == s.Installation.ID {
		t.Fatalf("resource not retained: %+v", replacement)
	}
	var key uuid.UUID
	if err = r.pool.QueryRow(t.Context(), `SELECT source_key FROM library_storage_locations WHERE id=$1`, location).Scan(&key); err != nil || key != s.Source.Key {
		t.Fatalf("lost binding: %v", err)
	}
	if _, err = r.Snapshot(t.Context(), s.Source.Key, s.Source.OwnerID); err != nil {
		t.Fatal(err)
	}
	// A live source cannot be taken over by another install, even with matching owner.
	if _, err = r.Install(t.Context(), req); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("live source replaced: %v", err)
	}
}

// The commit seam affects only the acknowledgement: PostgreSQL commits all
// Install writes before the caller receives a synthetic transport error.
func TestNativeStorageRegistryInstallLostCommitAcknowledgement(t *testing.T) {
	r, _, req := nativeRegistryFixture(t)
	req.Source.Key = uuid.New()
	lostAck := errors.New("synthetic lost COMMIT acknowledgement")
	r.commitInstall = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return lostAck
	}
	got, err := r.Install(t.Context(), req)
	if got != nil || !errors.Is(err, lostAck) {
		t.Fatalf("Install did not return ambiguous commit error: %v", err)
	}
	// A fresh instance observes the real committed source, marker, archive and
	// encrypted configuration, rather than an in-memory success result.
	fresh := &NativeStorageRegistry{pool: r.pool, configs: r.configs, baseDir: r.baseDir, approved: r.approved}
	snapshot, err := fresh.Snapshot(t.Context(), req.Source.Key, req.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != 1 || snapshot.Source.ConfigurationRevision != 1 ||
		len(snapshot.Config) != 1 || snapshot.Config[0].Value.AsMap()["bucket"] != "fixture" {
		t.Fatal("committed installation snapshot is incomplete")
	}
	binary, err := os.ReadFile(snapshot.Installation.InstallPath)
	if err != nil {
		t.Fatalf("committed executable was removed after lost acknowledgement: %v", err)
	}
	if !bytes.Equal(binary, req.Binary) {
		t.Fatal("committed executable bytes changed")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(snapshot.Installation.InstallPath), "manifest.json")); err != nil {
		t.Fatalf("committed manifest was removed: %v", err)
	}
}

func TestNativeStorageRegistryInstallKnownCommitRollback(t *testing.T) {
	r, _, req := nativeRegistryFixture(t)
	req.Source.Key = uuid.New()
	var installationID int64
	r.commitInstall = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT installation_id FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&installationID); err != nil {
			t.Fatal(err)
		}
		// Break the actual transaction so PostgreSQL answers COMMIT with
		// ROLLBACK and pgx returns its positive rollback sentinel.
		if _, err := tx.Exec(ctx, "SELECT 1/0"); err == nil {
			t.Fatal("fault injection failed to abort transaction")
		}
		return tx.Commit(ctx)
	}
	got, err := r.Install(t.Context(), req)
	if got != nil || !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("expected known commit rollback: %v", err)
	}
	var sources int
	if err = r.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("rolled back source remains: %d %v", sources, err)
	}
	var installations, markers, archives, configs int
	if err = r.pool.QueryRow(t.Context(), `SELECT
        (SELECT count(*) FROM plugin_installations WHERE id=$1),
        (SELECT count(*) FROM bloem_storage_installations WHERE installation_id=$1),
        (SELECT count(*) FROM plugin_archives WHERE plugin_installation_id=$1),
        (SELECT count(*) FROM plugin_runtime_configs WHERE plugin_installation_id=$1)`, installationID).Scan(&installations, &markers, &archives, &configs); err != nil {
		t.Fatal(err)
	}
	if installations != 0 || markers != 0 || archives != 0 || configs != 0 {
		t.Fatal("known rollback left partial installation records")
	}
	dirs, err := os.ReadDir(r.baseDir)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("known rollback leaked package: %d %v", len(dirs), err)
	}
}

func TestNativeStorageRegistryUpgradeKeepsSource(t *testing.T) {
	r, s, req := nativeRegistryFixture(t)
	allow := func(context.Context, pgx.Tx) error { return nil }
	next := append(append([]byte(nil), req.Binary...), []byte("upgrade")...)
	sum := sha256.Sum256(next)
	a := approvedNativeFixture()
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum, a.Manifest.Version = a.Checksum, "1.1.0"
	r.approved["next"] = a
	other := a
	other.Manifest = proto.Clone(a.Manifest).(*publicv1.PluginManifest)
	other.Manifest.PluginId = "bloem.storage.other"
	r.approved["other"] = other

	if _, err := r.pool.Exec(t.Context(), `INSERT INTO media_folders(id,type,name) VALUES(91920,'ebooks','synthetic upgrade fixture')`); err != nil {
		t.Fatal(err)
	}
	location := uuid.New()
	if _, err := r.pool.Exec(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,91920)`, location, s.Source.Key); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		key    string
		binary []byte
	}{"checksum": {"next", req.Binary}, "plugin": {"other", next}, "unapproved": {"missing", next}} {
		if _, err := r.UpgradeAuthorized(t.Context(), s.Installation.ID, s.Source.Key, s.Source.OwnerID, tc.key, tc.binary, allow); err == nil {
			t.Fatalf("%s: upgrade accepted", name)
		}
	}
	denied := errors.New("denied")
	if _, err := r.UpgradeAuthorized(t.Context(), s.Installation.ID, s.Source.Key, s.Source.OwnerID, "next", next, func(context.Context, pgx.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatalf("authorization ignored: %v", err)
	}

	upgraded, err := r.UpgradeAuthorized(t.Context(), s.Installation.ID, s.Source.Key, s.Source.OwnerID, "next", next, allow)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Installation.ID != s.Installation.ID || upgraded.Source.Key != s.Source.Key || upgraded.Generation != s.Generation+1 ||
		upgraded.Source.ConfigurationRevision != s.Source.ConfigurationRevision || upgraded.ArtifactChecksum != a.Checksum {
		t.Fatalf("upgrade snapshot %+v", upgraded)
	}
	if len(upgraded.Config) != 1 || upgraded.Config[0].Value.AsMap()["secret"] != "synthetic-only-secret" {
		t.Fatal("configuration not kept")
	}
	got, err := os.ReadFile(upgraded.Installation.InstallPath)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("executable not replaced: %v", err)
	}
	var version string
	var key uuid.UUID
	if err = r.pool.QueryRow(t.Context(), `SELECT i.version, l.source_key FROM plugin_installations i, library_storage_locations l WHERE i.id=$1 AND l.id=$2`,
		s.Installation.ID, location).Scan(&version, &key); err != nil || version != "1.1.0" || key != s.Source.Key {
		t.Fatalf("version %q location %s: %v", version, key, err)
	}
}

func TestNativeStorageRegistrySnapshotValidatesEachArchiveOnce(t *testing.T) {
	r, s, _ := nativeRegistryFixture(t)
	for range 2 {
		if _, err := r.Snapshot(t.Context(), s.Source.Key, s.Source.OwnerID); err != nil {
			t.Fatal(err)
		}
	}
	r.validatedMu.Lock()
	identity, ok := r.validatedArchives[s.Installation.ID]
	r.validatedMu.Unlock()
	if !ok || identity.checksum != s.ArtifactChecksum || identity.xmin == "" {
		t.Fatalf("validated archive not remembered: %+v %v", identity, ok)
	}
	// Any write to the row, even one leaving every other column alone, is a
	// new archive and is validated again.
	if _, err := r.pool.Exec(t.Context(), `UPDATE plugin_archives SET archive_bytes='tampered'::bytea WHERE plugin_installation_id=$1`, s.Installation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(t.Context(), s.Source.Key, s.Source.OwnerID); err == nil {
		t.Fatal("rewritten archive not validated")
	}
}
