//go:build integration

package libraryingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

const consumerSourceSchema = `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`

func consumerApprovedManifest(checksum string) *publicv1.PluginManifest {
	return &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: consumerSourceSchema}}}
}

// This executes the actual RPC executable and exact manifest check, so omission
// of the embedded schema fails before Configure or any publication.
func TestNativeOnboardingConsumerArtifactManifest(t *testing.T) {
	binary := consumerExecutable(t)
	checksum := sha256.Sum256(binary)
	path := filepath.Join(t.TempDir(), "consumer")
	if err := os.WriteFile(path, binary, 0700); err != nil {
		t.Fatal(err)
	}
	manager := storageplugin.NewManager(storageplugin.Config{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	value, err := structpb.NewStruct(map[string]any{"mode": "success", "revision": "v1", "notify": filepath.Join(t.TempDir(), "notify")})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Ensure(t.Context(), storageplugin.Snapshot{InstallationID: 1, Generation: 1, Enabled: true, NativeOnly: true, BinaryPath: path,
		ExpectedChecksum: hex.EncodeToString(checksum[:]), Manifest: consumerApprovedManifest(hex.EncodeToString(checksum[:])),
		Config: []*publicv1.ConfigEntry{{Key: "source", Value: value}}})
	if err != nil {
		t.Fatalf("actual approved source-schema artifact must start: %v", err)
	}
	described, err := session.Provider().Describe(t.Context(), &storagev1.DescribeRequest{})
	if err != nil || len(described.Sources) != 1 || described.Sources[0].Id != "books" || !described.Sources[0].RevisionPinnedReads {
		t.Fatalf("actual declared configuration was not consumed: %v", err)
	}
}

func consumerCurrentActor(t *testing.T, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
	t.Helper()
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	user, err := users.Create(ctx, models.CreateUserInput{Username: "a-current-admin", Email: "a-current-admin@example.test", Password: "domain-fixture-password", Role: "admin"})
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

// CURRENT positive experiment on frozen B production interfaces, pending B's
// independent review. The real RPC, SQL authorizer and publisher are unchanged.
func TestNativeOnboardingPublicationDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	result, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal("actual RPC/consumer/scanner publication", err)
	}
	if result.ScanResult.New != 2 || result.ScanResult.Unchanged != 3 || x.catalogFiles(t) != 2 {
		t.Fatalf("actual EPUB/PDF publication counts: %+v", result.ScanResult)
	}
	var snapshot string
	query := `SELECT jsonb_agg((to_jsonb(f)-'updated_at') ORDER BY f.id)::text FROM media_files f WHERE media_folder_id=$1`
	if err = x.pool.QueryRow(t.Context(), query, x.folder.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var containers, refs, permits, checkpoints int
	if err = x.pool.QueryRow(t.Context(), `SELECT (SELECT count(DISTINCT container) FROM media_files WHERE media_folder_id=$1),(SELECT count(*) FROM bloem_storage_file_refs WHERE binding_id=$2),(SELECT count(*) FROM bloem_native_publication_permits),(SELECT count(*) FROM bloem_storage_ingestion WHERE binding_id=$2 AND complete)`, x.folder.ID, x.binding.ID).Scan(&containers, &refs, &permits, &checkpoints); err != nil {
		t.Fatal(err)
	}
	if containers != 2 || refs != 2 || permits != 0 || checkpoints != 1 {
		t.Fatalf("incomplete publication: formats=%d refs=%d permits=%d checkpoints=%d", containers, refs, permits, checkpoints)
	}
	consumerSQL(t, x.pool, "UPDATE bloem_storage_ingestion SET complete=false,last_entry_id='',lease_until=clock_timestamp()-interval '1 second' WHERE binding_id=$1", x.binding.ID)
	replay, err := x.c.IngestNativeFolder(t.Context(), x.folder)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ScanResult.New != 0 || replay.ScanResult.Updated != 0 || replay.ScanResult.Unchanged != 5 {
		t.Fatalf("unchanged replay counts: %+v", replay.ScanResult)
	}
	var after string
	if err = x.pool.QueryRow(t.Context(), query, x.folder.ID).Scan(&after); err != nil || after != snapshot {
		t.Fatal("unchanged replay changed file identity/state", err)
	}
	t.Log("actual Install/Create/Initialize/Bind -> RPC -> RequireNativeScanTx -> EPUB/PDF permits/ref/checkpoint COMMIT; unchanged replay stable")
}

func TestNativeOnboardingConsumerConfigurationAdmissionDB(t *testing.T) {
	x := newConsumerFixture(t, "success")
	var before int
	if err := x.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_sources").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		config map[string]map[string]any
	}{
		{"undeclaredConfig", map[string]map[string]any{"undeclared": {"mode": "success"}}},
		{"unknownSourceProperty", map[string]map[string]any{"source": {"mode": "success", "unknown": "x"}}},
		{"wrongSourcePropertyType", map[string]map[string]any{"source": {"mode": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := x.management.Install(t.Context(), x.actor, nativestorage.InstallCommand{ArtifactKey: "fixture", Binary: consumerExecutable(t), ProviderSourceID: "other", RootEntryID: "root", Enabled: true, Config: tc.config})
			var typed *catalog.NativeOnboardingError
			if !errors.As(err, &typed) || typed.Code != "invalid_request" {
				t.Fatalf("invalid declared-source config must refuse invalid_request, got %T %v", err, err)
			}
		})
	}
	var after int
	if err := x.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_sources").Scan(&after); err != nil || before != after || x.catalogFiles(t) != 0 {
		t.Fatal("config admission mutated source/catalog", err)
	}
}
