//go:build integration

package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func nativeCurrentActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
	t.Helper()
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	tenants := tenancy.NewStore(pool)
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username='a-current-scanner-admin'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	user, err := users.GetByUsername(ctx, "a-current-scanner-admin")
	if count == 0 {
		user, err = users.Create(ctx, models.CreateUserInput{Username: "a-current-scanner-admin", Email: "a-current-scanner-admin@example.test", Password: "domain-fixture-password", Role: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tenants.ActivateInitialOwnership(ctx, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil {
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

// Scanner-only parsed-entry controls use actual domain commands/retained rows;
// the actual RPC executable chain is independently exercised by libraryingest.
func nativeCurrentLifecycle(t *testing.T, pool *pgxpool.Pool) (storagesource.SourceConfig, storagesource.Binding, *models.MediaFolder) {
	t.Helper()
	actor, _ := nativeCurrentActor(t, pool)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.a-current.scanner." + uuid.NewString(), Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object","additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("a-current-scanner-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"scanner": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := nativestorage.NewSourceManagement(pool, registry).Install(t.Context(), actor, nativestorage.InstallCommand{ArtifactKey: "scanner", ProviderSourceID: "books", RootEntryID: "root", Enabled: true, Binary: binary, Config: map[string]map[string]any{"storage": {}}})
	if err != nil {
		t.Fatal("actual Install", err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", source.SourceKey).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(t.Context(), source.SourceKey, owner)
	if err != nil {
		t.Fatal(err)
	}
	folders := catalog.NewFolderRepository(pool)
	libraries := nativestorage.NewLibraryManagement(pool, folders, sections.NewRepository(pool), resourcetenancy.NewStore(pool), nil)
	l1, err := libraries.Create(t.Context(), actor, nativestorage.LibraryCreateCommand{Name: "Current scanner", MetadataLanguage: "en"})
	if err != nil {
		t.Fatal("actual Create", err)
	}
	l2, err := libraries.Initialize(t.Context(), actor, l1.LibraryID, l1.LibraryRevision)
	if err != nil {
		t.Fatal("actual Initialize", err)
	}
	bound, err := libraries.Bind(t.Context(), actor, source.SourceKey, l2.LibraryID, source.ConfigurationRevision, l2.LibraryRevision)
	if err != nil {
		t.Fatal("actual Bind", err)
	}
	if bound.FolderID != l1.LibraryID || l2.CreationKey != l1.CreationKey {
		t.Fatal("identity replaced")
	}
	binding, ok, err := storagesource.NewRepository(pool).FolderBinding(t.Context(), l1.LibraryID)
	if err != nil || !ok {
		t.Fatal("binding absent", err)
	}
	folder, err := folders.GetByID(t.Context(), l1.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Source, binding, folder
}
func (x *nativeIngestFixture) authorize(ctx context.Context, tx pgx.Tx) error {
	return resourcetenancy.NewStore(x.pool).RequireNativeScanTx(ctx, tx, x.binding, x.source)
}
