//go:build integration

package plugins_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Wrong guarded codes or partial publication must fail real InstallAuthorized
// calls. Approvals are independently constructor-validated; no provider launches.
func TestNativeOnboardingSourceGuardedInstallWireDB(t *testing.T) {
	pool := nativeManagementDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{Username: "registry-domain-admin", Email: "registry-domain-admin@example.test", Password: "registry-fixture-password", Role: "admin"})
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	if _, err = tenancy.NewStore(pool).ActivateInitialOwnership(ctx, user.ID); err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("registry-domain-fixture", time.Hour, 24*time.Hour)
	svc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, actualUser, err := svc.Login(ctx, user.Username, "registry-fixture-password", "registry-domain", "127.0.0.1")
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	tokens := auth.NewAdminContextTokenService("registry-domain-fixture")
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: actualUser.ID, AccountIncarnationID: actualUser.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	actor, err := tokens.Parse(token)
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	resources := resourcetenancy.NewStore(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Error("actual guarded wire fixture rollback failed")
		}
	}()
	owner, err := resources.RequireNativeLibraryCreateTx(ctx, tx, actor, nil)
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	checksum := sha256.Sum256(binary)
	digest := hex.EncodeToString(checksum[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.storage.registry-domain", Version: "1.0.0", SiloApiVersion: "v1", Checksum: digest, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", Required: true, JsonSchema: `{"type":"object","properties":{"credential":{"type":"string"}},"required":["credential"],"additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("registry-test-key", 3)))
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	baseDir := t.TempDir()
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, baseDir, map[string]plugins.NativeStorageArtifact{"registry": {Manifest: manifest, Checksum: digest, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal("actual guarded wire fixture setup failed")
	}
	req := plugins.NativeStorageInstallRequest{ArtifactKey: "registry", Binary: binary, Source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: owner.ID, ProviderSourceID: "books", RootEntryID: "root", Enabled: true}, Config: map[string]map[string]any{"storage": {"credential": "registry-fixture-only"}}}
	authorizeCalls := 0
	authorizeInstall := func(ctx context.Context, tx pgx.Tx) error {
		authorizeCalls++
		actual, err := resources.RequireNativeLibraryCreateTx(ctx, tx, actor, nil)
		if err != nil {
			return err
		}
		if actual.ID != owner.ID {
			return resourcetenancy.ErrResourceHidden
		}
		return nil
	}

	state := func() string { return nativeWireLogicalState(t, pool) }
	requireCode := func(t *testing.T, err error, want string) {
		t.Helper()
		var typed *catalog.NativeOnboardingError
		mapped := catalog.MapNativeOnboardingError(err)
		if !errors.As(mapped, &typed) || typed.Code != want {
			got := ""
			if typed != nil {
				got = typed.Code
			}
			t.Errorf("actual guarded code=%s want=%s", got, want)
		}
	}
	requirePackages := func(t *testing.T, root string, want int) {
		t.Helper()
		entries, e := os.ReadDir(root)
		if e != nil || len(entries) != want {
			t.Error("private package count differs after actual operation")
		}
	}
	newApproval := func(data []byte) plugins.NativeStorageArtifact {
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		m := proto.CloneOf(manifest)
		m.Checksum = h
		return plugins.NativeStorageArtifact{Manifest: m, Checksum: h, OS: runtime.GOOS, Arch: runtime.GOARCH}
	}
	// Same complete ELF with only its independently parsed machine changed.
	wrong := append([]byte(nil), binary...)
	f, e := elf.NewFile(bytes.NewReader(wrong))
	if e != nil {
		t.Fatal("CURRENT test executable must be ELF for bounded wrong-machine fixture")
	}
	machine := elf.EM_AARCH64
	if f.Machine == machine {
		machine = elf.EM_X86_64
	}
	f.ByteOrder.PutUint16(wrong[18:20], uint16(machine))
	parsed, e := elf.NewFile(bytes.NewReader(wrong))
	if e != nil || parsed.Machine == f.Machine {
		t.Fatal("checksum-matching wrong-machine fixture invalid")
	}
	invalid := []byte("not an executable")
	wrongRegistry, e := plugins.NewNativeStorageRegistry(pool, cipher, baseDir, map[string]plugins.NativeStorageArtifact{"registry": newApproval(wrong)})
	if e != nil {
		t.Fatal("independent wrong-machine immutable approval rejected at constructor")
	}
	invalidRegistry, e := plugins.NewNativeStorageRegistry(pool, cipher, baseDir, map[string]plugins.NativeStorageArtifact{"registry": newApproval(invalid)})
	if e != nil {
		t.Fatal("independent nonexecutable immutable approval rejected at constructor")
	}
	unusable := filepath.Join(t.TempDir(), "unusable-install-root")
	if e = os.WriteFile(unusable, []byte("private regular file"), 0600); e != nil {
		t.Fatal("private FS refusal setup failed")
	}
	fsRegistry, e := plugins.NewNativeStorageRegistry(pool, cipher, unusable, map[string]plugins.NativeStorageArtifact{"registry": newApproval(binary)})
	if e != nil {
		t.Fatal("FS refusal constructor failed")
	}
	for _, tc := range []struct {
		name, want   string
		registry     *plugins.NativeStorageRegistry
		change       func(*plugins.NativeStorageInstallRequest)
		nilAuthorize bool
	}{
		{"unknown-approval", "artifact_rejected", registry, func(r *plugins.NativeStorageInstallRequest) { r.ArtifactKey = "unapproved" }, false},
		{"changed-bytes", "artifact_rejected", registry, func(r *plugins.NativeStorageInstallRequest) {
			r.Binary = append([]byte(nil), binary...)
			r.Binary[len(r.Binary)-1] ^= 1
		}, false},
		{"checksum-matching-nonexecutable", "artifact_rejected", invalidRegistry, func(r *plugins.NativeStorageInstallRequest) { r.Binary = invalid }, false},
		{"checksum-matching-wrong-machine", "artifact_rejected", wrongRegistry, func(r *plugins.NativeStorageInstallRequest) { r.Binary = wrong }, false},
		{"required-config", "artifact_rejected", registry, func(r *plugins.NativeStorageInstallRequest) { r.Config = nil }, false},
		{"undeclared-config", "artifact_rejected", registry, func(r *plugins.NativeStorageInstallRequest) {
			r.Config = map[string]map[string]any{"storage": {"credential": "fixture"}, "other": {}}
		}, false},
		{"schema-config", "artifact_rejected", registry, func(r *plugins.NativeStorageInstallRequest) {
			r.Config = map[string]map[string]any{"storage": {"credential": 7}}
		}, false},
		{"malformed-key", "invalid_request", registry, func(r *plugins.NativeStorageInstallRequest) { r.Config = map[string]map[string]any{"bad\x00key": {}} }, false},
		{"unsupported-host-shape", "invalid_request", registry, func(r *plugins.NativeStorageInstallRequest) {
			r.Config = map[string]map[string]any{"storage": {"credential": make(chan int)}}
		}, false},
		{"serialized-config-bound", "request_too_large", registry, func(r *plugins.NativeStorageInstallRequest) {
			r.Config = map[string]map[string]any{"storage": {"credential": 7, "extra": strings.Repeat("x", 1<<20)}}
		}, false},
		{"private-FS-failure", "native_storage_unavailable", fsRegistry, nil, false},
		{"nil-authorizer", "native_storage_unavailable", registry, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			r.Source.Key = uuid.New()
			if tc.change != nil {
				tc.change(&r)
			}
			before := state()
			calls := authorizeCalls
			authorize := plugins.NativeStorageAuthorizeTx(authorizeInstall)
			if tc.nilAuthorize {
				authorize = nil
			}
			snapshot, err := tc.registry.InstallAuthorized(ctx, r, authorize)
			requireCode(t, err, tc.want)
			if snapshot != nil || err == nil {
				t.Error("refused install returned a partial/success snapshot")
			}
			if state() != before {
				t.Error("refused install changed installation/marker/archive/config/source or namespace rows")
			}
			requirePackages(t, baseDir, 0)
			if authorizeCalls != calls {
				t.Error("pretransaction rejection unexpectedly reached authority callback")
			}
		})
	}
	// Legal guarded install reaches real durable authority and commits all owned
	// rows/package; that authority is never inferred from a nil callback.
	beforeCalls := authorizeCalls
	installed, err := registry.InstallAuthorized(ctx, req, authorizeInstall)
	if err != nil || installed == nil || installed.Installation == nil || installed.Source.Key != req.Source.Key || installed.Source.ConfigurationRevision != 1 || installed.Generation != 1 || authorizeCalls-beforeCalls < 2 {
		t.Fatal("legal guarded install did not retain actual authority/commit")
	}
	requirePackages(t, baseDir, 1)
	var publication int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM plugin_installations i JOIN bloem_storage_installations m ON m.installation_id=i.id JOIN plugin_archives a ON a.plugin_installation_id=i.id JOIN plugin_runtime_configs c ON c.plugin_installation_id=i.id JOIN bloem_storage_sources s ON s.installation_id=i.id WHERE s.key=$1 AND s.latest_installation_id=i.id`, req.Source.Key).Scan(&publication); e != nil || publication != 1 {
		t.Fatal("legal install atomic publication absent")
	}
	if data, e := os.ReadFile(installed.Installation.InstallPath); e != nil || sha256.Sum256(data) != checksum {
		t.Fatal("legal install package does not match supplied immutable bytes")
	}
	id := int64(installed.Installation.ID)
	authorizeExisting := func(ctx context.Context, tx pgx.Tx) error {
		return resources.RequireNativeManagementTx(ctx, tx, actor, req.Source.Key, &id, owner.ID, nil, true)
	}
	for _, tc := range []struct {
		name, want string
		config     map[string]map[string]any
	}{
		{"replacement-required", "invalid_request", nil},
		{"replacement-undeclared", "invalid_request", map[string]map[string]any{"storage": {"credential": "fixture"}, "other": {}}},
		{"replacement-schema", "invalid_request", map[string]map[string]any{"storage": {"credential": 7}}},
		{"replacement-host-shape", "invalid_request", map[string]map[string]any{"storage": {"credential": make(chan int)}}},
		{"replacement-bound-before-schema", "request_too_large", map[string]map[string]any{"storage": {"credential": 7, "extra": strings.Repeat("x", 1<<20)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := state()
			revision, err := registry.ReplaceConfigurationAuthorized(ctx, req.Source.Key, owner.ID, 1, tc.config, authorizeExisting)
			requireCode(t, err, tc.want)
			if err == nil || revision != 0 || state() != before {
				t.Error("replacement rejection wrote partial state/S/generation")
			}
			requirePackages(t, baseDir, 1)
		})
	}
	revision, err := registry.ReplaceConfigurationAuthorized(ctx, req.Source.Key, owner.ID, 1, req.Config, authorizeExisting)
	if err != nil || revision != 2 {
		t.Fatal("legal empty namespace replacement refused")
	}
	// Existing trusted nil-authorizer path remains legal with its existing config
	// behavior, including undeclared but host-shaped values.
	trusted := req
	trusted.Source.Key = uuid.New()
	trusted.Config = map[string]map[string]any{"legacy": {"credential": "fixture"}}
	trustedSnapshot, err := registry.Install(ctx, trusted)
	if err != nil || trustedSnapshot == nil {
		t.Fatal("trusted nil-authorizer path changed")
	}
	requirePackages(t, baseDir, 2)
	if err = sessions.Revoke(ctx, actor.SessionID); err != nil {
		t.Fatal("actual login revocation failed")
	}
	before := state()
	calls := authorizeCalls
	denied := req
	denied.Source.Key = uuid.New()
	snapshot, err := registry.InstallAuthorized(ctx, denied, authorizeInstall)
	if snapshot != nil || !errors.Is(err, resourcetenancy.ErrInvalidActor) || authorizeCalls == calls || state() != before {
		t.Fatal("actual revoked login passed guarded registry or changed full state")
	}
	requirePackages(t, baseDir, 2) // Pre-COMMIT package removed; prior packages retained.
	t.Log("actual guarded rejection codes/full-state/package cleanup; independent matching approvals; legal guarded/trusted install and empty replacement; actual revoked-login refusal")
}

func nativeWireLogicalState(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	result := ""
	for _, table := range []string{"plugin_installations", "bloem_storage_installations", "plugin_archives", "plugin_runtime_configs", "bloem_storage_sources", "bloem_storage_bindings", "bloem_storage_entries", "bloem_storage_file_refs", "bloem_storage_scan_runs", "bloem_storage_ingestion", "bloem_native_libraries", "media_folders", "media_folder_paths", "organization_entitlements"} {
		var digest string
		query := "SELECT md5(COALESCE(string_agg(rowdata,',' ORDER BY rowdata),'')) FROM (SELECT to_jsonb(t)::text rowdata FROM " + pgx.Identifier{table}.Sanitize() + " t) rows"
		if err := pool.QueryRow(t.Context(), query).Scan(&digest); err != nil {
			t.Fatal("private full-state snapshot failed")
		}
		result += digest + ";"
	}
	return result
}
