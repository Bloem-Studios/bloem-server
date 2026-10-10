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
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func nativeRegistryS3Fixture(t *testing.T, enabled bool) (*NativeStorageRegistry, *NativeStorageSnapshot, NativeStorageInstallRequest) {
	t.Helper()
	r, _, req := nativeRegistryFixture(t)
	a := r.approved["fixture"]
	a.Manifest = proto.CloneOf(a.Manifest)
	a.Manifest.PluginId = "bloem.storage.s3"
	a.Manifest.GlobalConfigSchema = []*publicv1.ConfigSchema{{Key: "connection", JsonSchema: `{"type":"object"}`}}
	r.approved["s3"] = a
	r.baseDir = nativeS3PrivateRoot(t)
	req.ArtifactKey = "s3"
	req.Source.Enabled = enabled
	req.Config = map[string]map[string]any{"connection": {"source_id": "synthetic-source", "secret_key": "synthetic-test-secret"}}
	s, err := r.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return r, s, req
}

func nativeS3SnapshotPath(t *testing.T, s *NativeStorageSnapshot) string {
	t.Helper()
	if s == nil || len(s.Config) != 1 || s.Config[0].GetKey() != "connection" {
		t.Fatal("S3 runtime configuration shape lost")
	}
	path := s.Config[0].Value.Fields["state_path"].GetStringValue()
	if !filepath.IsAbs(path) {
		t.Fatal("host-derived S3 state path absent")
	}
	return path
}

func TestNativeS3RegistryStateLifetime(t *testing.T) {
	r, installed, req := nativeRegistryS3Fixture(t, true)
	path := nativeS3SnapshotPath(t, installed)
	if _, supplied := req.Config["connection"]["state_path"]; supplied {
		t.Fatal("runtime path injection mutated caller settings")
	}
	callerConfig := map[string]map[string]any{"connection": {"state_path": "caller-controlled"}}
	for _, guarded := range []bool{false, true} {
		var err error
		if guarded {
			_, err = r.ReplaceConfigurationAuthorized(t.Context(), installed.Source.Key, installed.Source.OwnerID, 1, callerConfig, func(context.Context, pgx.Tx) error { return nil })
		} else {
			_, err = r.ReplaceConfiguration(t.Context(), installed.Source.Key, installed.Source.OwnerID, callerConfig)
		}
		var refusal *catalog.NativeOnboardingError
		if !errors.As(err, &refusal) || refusal.Code != "invalid_request" {
			t.Fatal("configuration writer delegated its state path to caller")
		}
	}
	badInstall := req
	badInstall.Config = callerConfig
	for _, guarded := range []bool{false, true} {
		var err error
		if guarded {
			_, err = r.InstallAuthorized(t.Context(), badInstall, func(context.Context, pgx.Tx) error { return nil })
		} else {
			_, err = r.Install(t.Context(), badInstall)
		}
		var refusal *catalog.NativeOnboardingError
		if !errors.As(err, &refusal) || refusal.Code != "invalid_request" {
			t.Fatal("installation writer delegated its state path to caller")
		}
	}
	if err := os.WriteFile(path, []byte("retained-fixture-state"), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(snapshot *NativeStorageSnapshot) {
		t.Helper()
		if nativeS3SnapshotPath(t, snapshot) != path {
			t.Fatal("configuration/release/runtime rotation changed durable S3 state path")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "retained-fixture-state" {
			t.Fatal("durable S3 state lost")
		}
		configs, err := r.configs.ListGlobalConfigs(t.Context(), installed.Installation.ID)
		if err != nil || len(configs) != 1 {
			t.Fatal("persisted configuration unavailable")
		}
		if _, persisted := configs[0].Value["state_path"]; persisted {
			t.Fatal("derived private path persisted in configuration/API view")
		}
	}
	restarted, err := NewNativeStorageRegistry(r.pool, r.configs.cipher, r.baseDir, r.approved)
	if err != nil {
		t.Fatal(err)
	}
	s, err := restarted.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	check(s)
	if _, err := r.ReplaceConfigurationAuthorized(t.Context(), installed.Source.Key, installed.Source.OwnerID, 1,
		map[string]map[string]any{"connection": {"source_id": "synthetic-source", "secret_key": "rotated-fixture-secret"}}, func(context.Context, pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s, err = restarted.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	check(s)
	next := append(append([]byte(nil), req.Binary...), []byte("s3-upgrade")...)
	a := r.approved["s3"]
	a.Manifest = proto.CloneOf(a.Manifest)
	sum := sha256.Sum256(next)
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum, a.Manifest.Version = a.Checksum, "1.1.0"
	r.approved["s3-next"] = a
	s, err = r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "s3-next", next, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	check(s)
	if s.Installation.InstallPath == installed.Installation.InstallPath {
		t.Fatal("upgrade fixture did not replace the executable directory")
	}
	second, err := r.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if nativeS3SnapshotPath(t, second) == path {
		t.Fatal("separate installations share key/cursor state")
	}
}

func TestNativeS3RegistryDisabledInstallState(t *testing.T) {
	r, installed, req := nativeRegistryS3Fixture(t, false)
	path := nativeS3SnapshotPath(t, installed)
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal("disabled install lacks private durable state directory")
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatal("disabled S3 installation became runnable")
	}
	upgraded, err := r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "s3", req.Binary, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal("disabled package-only S3 upgrade returned an error")
	}
	if upgraded.Source.Enabled || upgraded.Installation.Enabled || upgraded.Generation != 2 || nativeS3SnapshotPath(t, upgraded) != path {
		t.Fatal("disabled S3 upgrade lost state or enabled runtime")
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatal("disabled S3 upgrade admitted runtime")
	}
}

func TestNativeStorageRegistryDisabledPackageUpgrade(t *testing.T) {
	r, _, req := nativeRegistryFixture(t)
	req.Source.Enabled = false
	installed, err := r.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "fixture", req.Binary, func(context.Context, pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal("committed disabled package upgrade returned failure")
	}
	if upgraded.Generation != 2 || upgraded.Source.Enabled || upgraded.Installation.Enabled {
		t.Fatal("disabled package upgrade changed runtime admission")
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatal("disabled package upgrade admitted process snapshot")
	}
}

func TestNativeS3RegistryRejectsHistoricalPathsAndMalformedShape(t *testing.T) {
	r, installed, req := nativeRegistryS3Fixture(t, true)
	for _, value := range []map[string]any{{"state_path": "caller-path"}, {"state_path": ""}, {"state_path": nil}} {
		if err := r.configs.PutGlobalConfig(t.Context(), installed.Installation.ID, "connection", value); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
			t.Fatal("historical caller-supplied path reached runtime")
		}
	}
	if _, err := r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "s3", req.Binary, func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("historical supplied S3 path accepted by upgrade")
	}
	var generation int64
	if err := r.pool.QueryRow(t.Context(), `SELECT runtime_generation FROM plugin_installations WHERE id=$1`, installed.Installation.ID).Scan(&generation); err != nil || generation != 1 {
		t.Fatal("historical supplied S3 path committed an upgrade before refusal")
	}
	if _, err := r.pool.Exec(t.Context(), `DELETE FROM plugin_runtime_configs WHERE plugin_installation_id=$1`, installed.Installation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
		t.Fatal("missing native connection reached runtime")
	}
	if err := r.configs.PutGlobalConfig(t.Context(), installed.Installation.ID, "source_json", map[string]any{"json": "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
		t.Fatal("legacy S3 envelope reached native runtime")
	}
}

func TestNativeS3RegistryRefusesUnsafeStateBeforeUpgrade(t *testing.T) {
	r, installed, req := nativeRegistryS3Fixture(t, true)
	path := nativeS3SnapshotPath(t, installed)
	if err := os.Chmod(filepath.Dir(path), 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
		t.Fatal("writable private state directory reached runtime")
	}
	next := append(append([]byte(nil), req.Binary...), []byte("unsafe-upgrade")...)
	a := r.approved["s3"]
	a.Manifest = proto.CloneOf(a.Manifest)
	sum := sha256.Sum256(next)
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum, a.Manifest.Version = a.Checksum, "1.1.0"
	r.approved["s3-next"] = a
	if _, err := r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "s3-next", next, func(context.Context, pgx.Tx) error { return nil }); err == nil {
		t.Fatal("unsafe S3 state accepted during upgrade")
	}
	var version string
	if err := r.pool.QueryRow(t.Context(), `SELECT version FROM plugin_installations WHERE id=$1`, installed.Installation.ID).Scan(&version); err != nil || version != "1.0.0" {
		t.Fatal("refused upgrade still committed its release")
	}
}

func TestNativeS3InstallRefusesUnsafeAncestors(t *testing.T) {
	r, _, req := nativeRegistryS3Fixture(t, true)
	for _, kind := range []string{"symlink-base", "symlink-ancestor", "writable-ancestor", "readable-base", "writable-state", "sticky-temp"} {
		t.Run(kind, func(t *testing.T) {
			root := nativeS3PrivateRoot(t)
			base := filepath.Join(root, "native")
			switch kind {
			case "symlink-base", "symlink-ancestor":
				target := filepath.Join(root, "target")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, base); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink-ancestor" {
					base = filepath.Join(base, "child")
				}
			case "writable-ancestor":
				if err := os.Chmod(root, 0770); err != nil {
					t.Fatal(err)
				}
			case "readable-base":
				if err := os.Mkdir(base, 0750); err != nil {
					t.Fatal(err)
				}
			case "writable-state":
				if err := os.MkdirAll(filepath.Join(base, "state"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(base, "state"), 0770); err != nil {
					t.Fatal(err)
				}
			case "sticky-temp":
				base = t.TempDir()
			}
			r.baseDir = base
			if _, err := r.Install(t.Context(), req); err == nil {
				t.Fatal("unsafe S3 state namespace accepted")
			}
		})
	}
}

func TestNativeS3SnapshotRefusesUnsafeStateFile(t *testing.T) {
	r, installed, _ := nativeRegistryS3Fixture(t, true)
	path := nativeS3SnapshotPath(t, installed)
	for _, kind := range []string{"symlink", "public-file", "hard-link"} {
		t.Run(kind, func(t *testing.T) {
			defer func() { _ = os.Remove(path) }()
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(r.baseDir, "outside.db"), path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("synthetic-state"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "public-file" {
					if err := os.Chmod(path, 0640); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Link(path, filepath.Join(r.baseDir, "other-state.db")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Snapshot(t.Context(), installed.Source.Key, installed.Source.OwnerID); err == nil {
				t.Fatal("unsafe state file reached S3 runtime")
			}
		})
	}
}

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
