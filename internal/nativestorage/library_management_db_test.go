//go:build integration

package nativestorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func nativeDomainDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	nativeDomainFullMigrationSnapshot(t)
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
		t.Fatal("invalid private fixture")
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
			t.Log("B private UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	expected := cfg.ConnConfig.Database
	suffix := strings.TrimPrefix(expected, "bloem_storage_test_acore_")
	id, err := uuid.Parse(suffix)
	if err != nil || id == uuid.Nil || expected == input.ConnConfig.Database || !strings.HasPrefix(expected, "bloem_storage_test_acore_") {
		t.Fatal("unowned clone identity")
	}
	cfg.MaxConns = 5
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded clone failed")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != expected || actual == input.ConnConfig.Database {
		t.Fatal("actual clone identity unverified")
	}
	t.Logf("B actual UUID target verified: %s; differs from private input", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != expected {
		t.Fatal("prepared pool identity changed")
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("CURRENT schema inventory not ready")
	}
	t.Logf("B prepared embedded migration SHA256 %x", compiledHash)
	return pool
}
func nativeDomainActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
	t.Helper()
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{Username: "native-domain-admin", Email: "native-domain-admin@example.test", Password: "domain-fixture-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	tenants := tenancy.NewStore(pool)
	if _, err = tenants.ActivateInitialOwnership(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("native-domain-fixture-signing", time.Hour, 24*time.Hour)
	svc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, actual, err := svc.Login(ctx, user.Username, "domain-fixture-password", "native-domain", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
	// Claims are derived from the actual enabled account and actual login session,
	// then round-tripped through the signed native administrative context service.
	platformToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	platform, err := tokens.Parse(platformToken)
	if err != nil {
		t.Fatal(err)
	}
	var orgID uuid.UUID
	if err = pool.QueryRow(ctx, "SELECT id FROM organizations WHERE is_default").Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	org, err := tenants.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := tenants.GetMembership(ctx, user.ID, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	orgToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID,
		Scope: auth.AdminScopeOrganization, OrganizationID: org.ID, MembershipID: member.ID, PolicyRevision: org.PolicyRevision, SecurityRevision: member.SecurityRevision, EffectiveAuthority: "organization_admin"})
	if err != nil {
		t.Fatal(err)
	}
	organization, err := tokens.Parse(orgToken)
	if err != nil {
		t.Fatal(err)
	}
	return platform, organization
}
func nativeDomainSourceFixture(t *testing.T, pool *pgxpool.Pool, actor auth.AdminContextClaims) (*SourceManagement, SourceView, InstallCommand) {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	m := &publicv1.PluginManifest{PluginId: "bloem.storage.domain-fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object","properties":{"credential":{"type":"string"}},"additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("domain-fixture-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, filepath.Join(t.TempDir(), "packages"), map[string]plugins.NativeStorageArtifact{"domain": {Manifest: m, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSourceManagement(pool, registry)
	cmd := InstallCommand{ArtifactKey: "domain", ProviderSourceID: "opaque-books", RootEntryID: "opaque-root", Enabled: true, Binary: binary, Config: map[string]map[string]any{"storage": {"credential": "fixture-only-value"}}}
	source, err := svc.Install(t.Context(), actor, cmd)
	if err != nil {
		t.Fatal(err)
	}
	return svc, source, cmd
}
func nativeDomainLibraries(pool *pgxpool.Pool) *LibraryManagement {
	return NewLibraryManagement(pool, catalog.NewFolderRepository(pool), sections.NewRepository(pool), resourcetenancy.NewStore(pool), nil)
}
func nativeDomainRequireCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *catalog.NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("code=%s expected; got %T %v", code, err, err)
	}
}
func TestNativeOnboardingLibraryLegalLifecycleDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	_, source, _ := nativeDomainSourceFixture(t, pool, actor)
	libs := nativeDomainLibraries(pool)
	created, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: " Books "})
	if err != nil {
		t.Fatalf("actual owned canonical Create must succeed: %v", err)
	}
	if created.LibraryID <= 0 || created.CreationKey == uuid.Nil || created.LibraryRevision != 1 || created.Initialized || created.State != "initialization_required" {
		t.Fatal("Create did not retain real L1 identity")
	}
	initialized, err := libs.Initialize(t.Context(), actor, created.LibraryID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if initialized.LibraryID != created.LibraryID || initialized.CreationKey != created.CreationKey || initialized.LibraryRevision != 2 || !initialized.Initialized || initialized.State != "unbound" {
		t.Fatal("Initialize did not retain L2 identity")
	}
	bound, err := libs.Bind(t.Context(), actor, source.SourceKey, created.LibraryID, source.ConfigurationRevision, 2)
	if err != nil {
		t.Fatal(err)
	}
	if bound.FolderID != created.LibraryID || bound.BindingID == uuid.Nil || bound.LibraryRevision != 3 || bound.SourceRevision != source.ConfigurationRevision || bound.Repeated {
		t.Fatal("Bind did not acknowledge actual L3 binding")
	}
	var actualID uuid.UUID
	var revision int64
	var pathCount int
	if err = pool.QueryRow(t.Context(), `SELECT b.id,n.revision,(SELECT count(*) FROM media_folder_paths WHERE media_folder_id=n.folder_id)
 FROM bloem_native_libraries n JOIN bloem_storage_bindings b ON b.folder_id=n.folder_id WHERE n.folder_id=$1`, created.LibraryID).Scan(&actualID, &revision, &pathCount); err != nil || actualID != bound.BindingID || revision != 3 || pathCount != 0 {
		t.Fatal("observed binding COMMIT/pathless L3 missing")
	}
	repeated, err := libs.Bind(t.Context(), actor, source.SourceKey, created.LibraryID, source.ConfigurationRevision, 2)
	if err != nil || !repeated.Repeated || repeated.BindingID != bound.BindingID {
		t.Fatal("exact lost-response repeat failed")
	}
	_, err = libs.Bind(t.Context(), actor, source.SourceKey, created.LibraryID, source.ConfigurationRevision, 1)
	nativeDomainRequireCode(t, err, "revision_conflict")
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer nativeDomainRollback(t.Context(), tx)
	if err = sections.NewRepository(pool).RequireNativeInitializationWitnessesTx(t.Context(), tx, created.LibraryID); err != nil {
		t.Fatal("actual section witnesses absent", err)
	}
	t.Log("LEGAL actor Install -> SAME-ID Create L1 -> actual section Initialize L2 -> unique binding COMMIT L3 observed")
}

// Test-only export lets external queue integration consume the actual domain
// lifecycle without introducing a production import of scanqueue.
func NativeDomainLegalFixtureForTest(t *testing.T) (*pgxpool.Pool, auth.AdminContextClaims, SourceView, int) {
	t.Helper()
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	_, source, _ := nativeDomainSourceFixture(t, pool, actor)
	libs := nativeDomainLibraries(pool)
	l1, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Queued Books"})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := libs.Initialize(t.Context(), actor, l1.LibraryID, l1.LibraryRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = libs.Bind(t.Context(), actor, source.SourceKey, l2.LibraryID, source.ConfigurationRevision, l2.LibraryRevision); err != nil {
		t.Fatal(err)
	}
	return pool, actor, source, l1.LibraryID
}

func TestNativeOnboardingLibraryCommitRecoveryDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	_, source, _ := nativeDomainSourceFixture(t, pool, actor)
	libs := nativeDomainLibraries(pool)
	lost := errors.New("synthetic commit acknowledgement loss")
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return lost
	}
	_, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Recover Create"})
	var unknown *MutationOutcomeUnknown
	if !errors.As(err, &unknown) || unknown.Operation != "create" || unknown.OperationID == uuid.Nil || unknown.LibraryID <= 0 || unknown.CreationKey == uuid.Nil || !errors.Is(err, lost) {
		t.Fatal("committed Create did not return typed recoverable uncertainty")
	}
	original := *unknown
	libs.commit = nil
	recovered, err := libs.GetByCreationKey(t.Context(), actor, original.CreationKey)
	if err != nil || recovered.LibraryID != original.LibraryID || recovered.Initialized || recovered.LibraryRevision != 1 {
		t.Fatal("Create reconciliation lost original L1 identity")
	}
	var before int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_native_libraries").Scan(&before); err != nil {
		t.Fatal(err)
	}
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
		return pgx.ErrTxCommitRollback
	}
	_, err = libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Rolled Back Create"})
	if errors.As(err, &unknown) {
		t.Fatal("definite Create rollback called unknown")
	}
	var after int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_native_libraries").Scan(&after); err != nil || after != before {
		t.Fatal("definite Create rollback left a marker")
	}
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if operation == "initialize" {
			if e := tx.Rollback(ctx); e != nil {
				return e
			}
			return pgx.ErrTxCommitRollback
		}
		return tx.Commit(ctx)
	}
	_, err = libs.Initialize(t.Context(), actor, recovered.LibraryID, 1)
	if err == nil || errors.As(err, &unknown) {
		t.Fatal("definite final initialization rollback acknowledged or called unknown")
	}
	partial, err := libs.Get(t.Context(), actor, recovered.LibraryID)
	if err != nil || partial.Initialized || partial.LibraryRevision != 1 {
		t.Fatal("partial sections promoted marker")
	}
	var sectionRowsBefore string
	if err = pool.QueryRow(t.Context(), `SELECT md5(string_agg(to_jsonb(p)::text,',' ORDER BY id)) FROM page_sections p`).Scan(&sectionRowsBefore); err != nil {
		t.Fatal(err)
	}
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		return lost
	}
	_, err = libs.Initialize(t.Context(), actor, recovered.LibraryID, 1)
	if !errors.As(err, &unknown) || unknown.Operation != "initialize" || unknown.LibraryID != recovered.LibraryID || unknown.CreationKey != recovered.CreationKey {
		t.Fatal("committed Initialize did not retain recovery identifiers")
	}
	libs.commit = nil
	l2, err := libs.Initialize(t.Context(), actor, recovered.LibraryID, 1)
	if err != nil || !l2.Initialized || l2.LibraryRevision != 2 {
		t.Fatal("same-ID Initialize recovery failed")
	}
	var sectionRowsAfter string
	if err = pool.QueryRow(t.Context(), `SELECT md5(string_agg(to_jsonb(p)::text,',' ORDER BY id)) FROM page_sections p`).Scan(&sectionRowsAfter); err != nil || sectionRowsBefore != sectionRowsAfter {
		t.Fatal("Initialize retries rewrote actual section rows")
	}
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if e := tx.Rollback(ctx); e != nil {
			return e
		}
		return pgx.ErrTxCommitRollback
	}
	_, err = libs.Bind(t.Context(), actor, source.SourceKey, recovered.LibraryID, source.ConfigurationRevision, 2)
	if err == nil || errors.As(err, &unknown) {
		t.Fatal("definite Bind rollback acknowledged or called unknown")
	}
	current, err := libs.Get(t.Context(), actor, recovered.LibraryID)
	if err != nil || current.LibraryRevision != 2 || current.BindingID != nil {
		t.Fatal("Bind rollback left L3 or binding")
	}
	libs.commit = func(ctx context.Context, tx pgx.Tx, operation string) error {
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		return lost
	}
	_, err = libs.Bind(t.Context(), actor, source.SourceKey, recovered.LibraryID, source.ConfigurationRevision, 2)
	if !errors.As(err, &unknown) || unknown.Operation != "bind" || unknown.SourceKey == nil || *unknown.SourceKey != source.SourceKey || unknown.LibraryID != recovered.LibraryID || unknown.CreationKey != recovered.CreationKey {
		t.Fatal("committed Bind lost authorized recovery identifiers")
	}
	libs.commit = nil
	bound, err := libs.Get(t.Context(), actor, recovered.LibraryID)
	if err != nil || bound.LibraryRevision != 3 || bound.BindingID == nil {
		t.Fatal("Bind reconciliation missed durable binding")
	}
	repeat, err := libs.Bind(t.Context(), actor, source.SourceKey, recovered.LibraryID, source.ConfigurationRevision, 2)
	if err != nil || !repeat.Repeated || repeat.BindingID != *bound.BindingID {
		t.Fatal("Bind recovery allocated another binding")
	}
	page, err := libs.List(t.Context(), actor, nil, 1)
	if err != nil || len(page.Libraries) != 1 || page.Libraries[0].LibraryID != recovered.LibraryID || page.NextAfter != nil {
		t.Fatal("lost-response list reconciliation created or omitted identity")
	}
	t.Log("actual commit+ackloss vs definite rollback verified for Create/Initialize/Bind; SAME-ID recovery preserved rows")
}

type nativeInitializationCommitTrace struct {
	sawSections     atomic.Bool
	fired           atomic.Bool
	revoke          func() error
	revocationError error
}
type nativeInitializationCommitTraceKey struct{}

func (q *nativeInitializationCommitTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "INSERT INTO page_sections") {
		q.sawSections.Store(true)
	}
	return context.WithValue(ctx, nativeInitializationCommitTraceKey{}, strings.EqualFold(strings.TrimSpace(data.SQL), "commit"))
}
func (q *nativeInitializationCommitTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	commit, _ := ctx.Value(nativeInitializationCommitTraceKey{}).(bool)
	if data.Err == nil && commit && q.sawSections.Load() && q.fired.CompareAndSwap(false, true) {
		q.revocationError = q.revoke()
	}
}
func TestNativeOnboardingLibraryStageRevocationDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	libs := nativeDomainLibraries(pool)
	l1, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Stage Authorization"})
	if err != nil {
		t.Fatal(err)
	}
	trace := &nativeInitializationCommitTrace{revoke: func() error { return auth.NewSessionRepository(pool).Revoke(t.Context(), actor.SessionID) }}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = trace
	var traced *pgxpool.Pool
	t.Cleanup(func() {
		if traced != nil {
			traced.Close()
		}
	})
	traced, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open guarded section consumer pool failed")
	}
	var actual string
	if err = traced.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != pool.Config().ConnConfig.Database {
		t.Fatal("actual section consumer pool identity unverified")
	}
	libs.sections = sections.NewRepository(traced)
	_, err = libs.Initialize(t.Context(), actor, l1.LibraryID, 1)
	nativeDomainRequireCode(t, err, "authorization_state_stale")
	if !trace.fired.Load() || trace.revocationError != nil {
		t.Fatal("real section commit revocation barrier not reached")
	}
	var initialized bool
	var revision int64
	var libraryRows, homeRows int
	if err = pool.QueryRow(t.Context(), `SELECT initialized,revision,
 (SELECT count(*) FROM page_sections WHERE scope='library' AND library_id=$1),
 (SELECT count(*) FROM page_sections WHERE scope='home' AND config->>'filter_library_id'=$2)
 FROM bloem_native_libraries WHERE folder_id=$1`, l1.LibraryID, fmt.Sprint(l1.LibraryID)).Scan(&initialized, &revision, &libraryRows, &homeRows); err != nil {
		t.Fatal(err)
	}
	if initialized || revision != 1 || libraryRows == 0 || homeRows != 0 {
		t.Fatal("revoked later stage discarded committed defaults or promoted marker")
	}
	// Re-login to the real enabled account and use its native platform context to
	// finish the same retained organization-owned ID, without replacing sections.
	users := auth.NewUserRepository(pool)
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("native-domain-fixture-signing", time.Hour, 24*time.Hour)
	authSvc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, user, err := authSvc.Login(t.Context(), "native-domain-admin", "domain-fixture-password", "stage-recovery", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
	token, err := tokens.Mint(auth.AdminContextClaims{AccountID: user.ID, AccountIncarnationID: user.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := tokens.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := libs.Initialize(t.Context(), fresh, l1.LibraryID, 1)
	if err != nil || !recovered.Initialized || recovered.LibraryRevision != 2 || recovered.CreationKey != l1.CreationKey {
		t.Fatal("fresh retained actor could not recover same-ID partial initialization")
	}
	t.Log("observed library-defaults COMMIT then real originating-session revocation; later stage refused; SAME-ID fresh-login recovery passed")
}

func TestNativeOnboardingLibraryUniqueBindingDB(t *testing.T) {
	pool := nativeDomainDatabase(t)
	_, actor := nativeDomainActor(t, pool)
	sources, first, cmd := nativeDomainSourceFixture(t, pool, actor)
	second, err := sources.Install(t.Context(), actor, cmd)
	if err != nil {
		t.Fatal(err)
	}
	libs := nativeDomainLibraries(pool)
	l1, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Unique Source Books"})
	if err != nil {
		t.Fatal(err)
	}
	l2, err := libs.Initialize(t.Context(), actor, l1.LibraryID, 1)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, source := range []SourceView{first, second} {
		go func() {
			<-start
			_, e := libs.Bind(t.Context(), actor, source.SourceKey, l2.LibraryID, 1, 2)
			results <- e
		}()
	}
	close(start)
	wins := 0
	for range 2 {
		e := <-results
		if e == nil {
			wins++
		} else {
			var typed *catalog.NativeOnboardingError
			if !errors.As(e, &typed) || (typed.Code != "native_storage_unavailable" && typed.Code != "binding_conflict" && typed.Code != "revision_conflict") {
				t.Fatal("unexpected concurrent binding loser", e)
			}
		}
	}
	if wins != 1 {
		t.Fatal("two different sources did not yield exactly one binding winner")
	}
	var count int
	var key uuid.UUID
	var revision int64
	if err = pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=$1),b.source_key,n.revision FROM bloem_storage_bindings b JOIN bloem_native_libraries n ON n.folder_id=b.folder_id WHERE b.folder_id=$1`, l1.LibraryID).Scan(&count, &key, &revision); err != nil || count != 1 || revision != 3 {
		t.Fatal("unique binding and L3 not atomically durable", err)
	}
	loser := first
	if key == first.SourceKey {
		loser = second
	}
	_, err = libs.Bind(t.Context(), actor, loser.SourceKey, l1.LibraryID, 1, 3)
	nativeDomainRequireCode(t, err, "binding_conflict")
	repeated, err := libs.Bind(t.Context(), actor, key, l1.LibraryID, 1, 2)
	if err != nil || !repeated.Repeated {
		t.Fatal("exact current binding recovery failed", err)
	}
	other, err := libs.Create(t.Context(), actor, LibraryCreateCommand{Name: "Independent Books"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = libs.Initialize(t.Context(), actor, other.LibraryID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = libs.Bind(t.Context(), actor, loser.SourceKey, other.LibraryID, 1, 2); err != nil {
		t.Fatal(err)
	}
	var groups int
	if err = pool.QueryRow(t.Context(), "SELECT count(DISTINCT id) FROM library_collection_groups WHERE label='user-collections' AND library_id=ANY($1::bigint[])", []int64{int64(l1.LibraryID), int64(other.LibraryID)}).Scan(&groups); err != nil || groups != 2 {
		t.Fatal("independent canonical library groups not retained", err)
	}
	t.Log("concurrent two-source unique Bind COMMIT winner; foreign retarget denied; independent library canonical groups retained")
}
