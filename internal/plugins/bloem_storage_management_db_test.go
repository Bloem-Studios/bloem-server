//go:build integration

package plugins_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeManagementDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	nativeManagementFullMigrationSnapshot(t)
	migrationPath := "sql/20261006145307_bloem_native_storage_onboarding.sql"
	embedded, err := migrations.FS.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	compiledHash := sha256.Sum256(embedded)
	diskBefore, err := os.ReadFile("../../migrations/" + migrationPath)
	if err != nil || sha256.Sum256(diskBefore) != compiledHash {
		t.Fatal("embedded/disk migration snapshot mismatch before gate")
	}
	t.Logf("B migration BEFORE embedded=disk SHA256 %x", compiledHash)
	t.Cleanup(func() {
		diskAfter, e := os.ReadFile("../../migrations/" + migrationPath)
		if e != nil {
			t.Error("read migration snapshot after gate failed")
			return
		}
		after := sha256.Sum256(diskAfter)
		t.Logf("B migration AFTER embedded=%x disk=%x stable=%t", compiledHash, after, after == compiledHash)
		if after != compiledHash {
			t.Error("migration changed during gate; scoped evidence provisional")
		}
	})
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	private := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(private)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("private fixture unavailable")
	}
	input, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("private fixture configuration invalid")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("B registry UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	expected := cfg.ConnConfig.Database
	id, err := uuid.Parse(strings.TrimPrefix(expected, "bloem_storage_test_acore_"))
	if err != nil || id == uuid.Nil || !strings.HasPrefix(expected, "bloem_storage_test_acore_") || expected == input.ConnConfig.Database {
		t.Fatal("unowned clone identity")
	}
	cfg.MaxConns = 4
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded clone failed")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != expected || actual == input.ConnConfig.Database {
		t.Fatal("actual registry consumer identity unverified")
	}
	t.Logf("B registry actual UUID target verified: %s; differs from private input", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("CURRENT full schema unavailable")
	}
	t.Logf("B registry prepared embedded migration SHA256 %x", compiledHash)
	return pool
}
func TestNativeOnboardingSourceRegistryCommitRecoveryDB(t *testing.T) {
	pool := nativeManagementDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{Username: "registry-domain-admin", Email: "registry-domain-admin@example.test", Password: "registry-fixture-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tenancy.NewStore(pool).ActivateInitialOwnership(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("registry-domain-fixture", time.Hour, 24*time.Hour)
	svc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, actualUser, err := svc.Login(ctx, user.Username, "registry-fixture-password", "registry-domain", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewAdminContextTokenService("registry-domain-fixture")
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: actualUser.ID, AccountIncarnationID: actualUser.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := tokens.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	resources := resourcetenancy.NewStore(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := resources.RequireNativeLibraryCreateTx(ctx, tx, actor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(binary)
	digest := hex.EncodeToString(checksum[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.storage.registry-domain", Version: "1.0.0", SiloApiVersion: "v1", Checksum: digest, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object"}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("registry-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, baseDir, map[string]plugins.NativeStorageArtifact{"registry": {Manifest: manifest, Checksum: digest, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	req := plugins.NativeStorageInstallRequest{ArtifactKey: "registry", Binary: binary, Source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: owner.ID, ProviderSourceID: "books", RootEntryID: "root", Enabled: true}, Config: map[string]map[string]any{"storage": {"credential": "registry-fixture-only"}}}
	authorizeInstall := func(ctx context.Context, tx pgx.Tx) error {
		actual, err := resources.RequireNativeLibraryCreateTx(ctx, tx, actor, nil)
		if err != nil {
			return err
		}
		if actual.ID != owner.ID {
			return resourcetenancy.ErrResourceHidden
		}
		return nil
	}
	lost := errors.New("synthetic installation commit acknowledgement loss")
	plugins.NativeManagementCommitHooksForTest(registry, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return lost
	}, nil)
	_, err = registry.InstallAuthorized(ctx, req, authorizeInstall)
	var unknown *catalog.MutationOutcomeUnknown
	if !errors.As(err, &unknown) || unknown.Operation != "install" || unknown.OperationID == uuid.Nil || unknown.SourceKey == nil || *unknown.SourceKey != req.Source.Key || unknown.LibraryID != 0 || !errors.Is(err, lost) {
		t.Fatal("install commit uncertainty lost authorized source/operation identity")
	}
	var installationID int64
	var installedPath string
	if err = pool.QueryRow(ctx, `SELECT i.id,i.install_path FROM plugin_installations i JOIN bloem_storage_sources s ON s.installation_id=i.id WHERE s.key=$1`, req.Source.Key).Scan(&installationID, &installedPath); err != nil {
		t.Fatal("committed install not reconcilable")
	}
	var latest *int64
	requireLineage := func() {
		t.Helper()
		if e := pool.QueryRow(ctx, "SELECT latest_installation_id FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&latest); e != nil || latest == nil || *latest != installationID {
			t.Fatal("committed latest marked installation witness not reconcilable", e)
		}
	}
	requireLineage()
	if data, e := os.ReadFile(installedPath); e != nil || sha256.Sum256(data) != checksum {
		t.Fatal("unknown install removed possibly committed package")
	}
	plugins.NativeManagementCommitHooksForTest(registry, func(ctx context.Context, tx pgx.Tx) error {
		if e := tx.Rollback(ctx); e != nil {
			return e
		}
		return pgx.ErrTxCommitRollback
	}, nil)
	rolledBack := req
	rolledBack.Source.Key = uuid.New()
	_, err = registry.InstallAuthorized(ctx, rolledBack, authorizeInstall)
	if err == nil || errors.As(err, &unknown) {
		t.Fatal("definite install rollback acknowledged or called unknown")
	}
	var exists bool
	if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM bloem_storage_sources WHERE key=$1)", rolledBack.Source.Key).Scan(&exists); err != nil || exists {
		t.Fatal("definite rollback retained source")
	}
	directories, err := os.ReadDir(baseDir)
	if err != nil || len(directories) != 1 {
		t.Fatal("known rollback retained orphaned package or cleaned original unknown package")
	}
	authorizeExisting := func(ctx context.Context, tx pgx.Tx) error {
		return resources.RequireNativeManagementTx(ctx, tx, actor, req.Source.Key, &installationID, owner.ID, nil, true)
	}
	plugins.NativeManagementCommitHooksForTest(registry, nil, func(ctx context.Context, tx pgx.Tx) error {
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		return lost
	})
	_, err = registry.ReplaceConfigurationAuthorized(ctx, req.Source.Key, owner.ID, 1, req.Config, authorizeExisting)
	if !errors.As(err, &unknown) || unknown.Operation != "configuration" || unknown.SourceKey == nil || *unknown.SourceKey != req.Source.Key {
		t.Fatal("configuration commit uncertainty lost source identity")
	}
	var revision, generation int64
	if err = pool.QueryRow(ctx, `SELECT s.configuration_revision,i.runtime_generation FROM bloem_storage_sources s JOIN plugin_installations i ON i.id=s.installation_id WHERE s.key=$1`, req.Source.Key).Scan(&revision, &generation); err != nil || revision != 2 || generation != 2 {
		t.Fatal("configuration commit not durably reconcilable")
	}
	requireLineage() // Configuration preserves the association.
	// Definite removal rollback must preserve every source field and installation
	// generation, including the witness written just before detach.
	var beforeSource, afterSource string
	if err = pool.QueryRow(ctx, "SELECT to_jsonb(s)::text FROM bloem_storage_sources s WHERE key=$1", req.Source.Key).Scan(&beforeSource); err != nil {
		t.Fatal(err)
	}
	plugins.NativeManagementCommitHooksForTest(registry, nil, func(ctx context.Context, tx pgx.Tx) error {
		if e := tx.Rollback(ctx); e != nil {
			return e
		}
		return pgx.ErrTxCommitRollback
	})
	err = registry.RemoveAuthorized(ctx, int(installationID), req.Source.Key, owner.ID, 2, false, authorizeExisting)
	if err == nil || errors.As(err, &unknown) {
		t.Fatal("definite Disable rollback acknowledged or called unknown")
	}
	if err = pool.QueryRow(ctx, "SELECT to_jsonb(s)::text FROM bloem_storage_sources s WHERE key=$1", req.Source.Key).Scan(&afterSource); err != nil || beforeSource != afterSource {
		t.Fatal("definite Disable rollback changed source/latest witness")
	}
	if err = pool.QueryRow(ctx, "SELECT runtime_generation FROM plugin_installations WHERE id=$1", installationID).Scan(&generation); err != nil || generation != 2 {
		t.Fatal("definite Disable rollback changed generation")
	}
	plugins.NativeManagementCommitHooksForTest(registry, nil, func(ctx context.Context, tx pgx.Tx) error {
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		return lost
	})
	// Lifecycle acknowledgement loss also returns retained source/operation IDs.
	err = registry.RemoveAuthorized(ctx, int(installationID), req.Source.Key, owner.ID, 2, false, authorizeExisting)
	if !errors.As(err, &unknown) || unknown.Operation != "disable" || unknown.SourceKey == nil || *unknown.SourceKey != req.Source.Key {
		t.Fatal("disable commit uncertainty lost source identity")
	}
	var enabled bool
	var attached *int64
	if err = pool.QueryRow(ctx, "SELECT configuration_revision,enabled,installation_id FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&revision, &enabled, &attached); err != nil || revision != 3 || enabled || attached != nil {
		t.Fatal("uncertain Disable not durably reconcilable")
	}
	requireLineage() // Actual Disable commit retains latest despite detachment.
	err = registry.RemoveAuthorized(ctx, int(installationID), req.Source.Key, owner.ID, 3, true, authorizeExisting)
	if !errors.As(err, &unknown) || unknown.Operation != "uninstall" || unknown.SourceKey == nil || *unknown.SourceKey != req.Source.Key {
		t.Fatal("uninstall commit uncertainty lost retained identity")
	}
	if err = pool.QueryRow(ctx, "SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&revision); err != nil || revision != 4 {
		t.Fatal("uncertain uninstall discarded source or failed S fence")
	}
	if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM plugin_installations WHERE id=$1)", installationID).Scan(&exists); err != nil || exists {
		t.Fatal("uncertain uninstall not durably reconcilable")
	}
	requireLineage() // Actual uninstall commit retains latest after FK deletion.
	if err = sessions.Revoke(ctx, actor.SessionID); err != nil {
		t.Fatal(err)
	}
	_, err = registry.ReplaceConfigurationAuthorized(ctx, req.Source.Key, owner.ID, 4, req.Config, authorizeExisting)
	if !errors.Is(err, resourcetenancy.ErrInvalidActor) {
		t.Fatal("revoked real login passed mandatory write-tx callback")
	}
	if err = pool.QueryRow(ctx, "SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1", req.Source.Key).Scan(&revision); err != nil || revision != 4 {
		t.Fatal("denied callback changed rows")
	}
	t.Log("actual registry COMMIT+ackloss retained package/source IDs; definite rollback cleaned only its package; fresh actor callback denied revoked login")
}

func nativeManagementFullMigrationSnapshot(t *testing.T) {
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
