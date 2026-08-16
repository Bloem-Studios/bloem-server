package audiobooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatcontract"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

func newABSIdentityDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate disposable database name: %v", err)
	}
	name := "abs_identity_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database %q: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	return pool
}

// TestAudiobookshelfIdentityContractAgainstRealHandler drives the frozen
// Audiobookshelf identity suite at the real ABS handler built exactly as
// production builds it — BuildABSHandler with the real SiloCredValidator,
// auth service, token store, and access resolver over a disposable
// PostgreSQL database. Logins mint real JWTs whose JTIs are validated
// per-request, and logout revokes them for real.
func TestAudiobookshelfIdentityContractAgainstRealHandler(t *testing.T) {
	ctx := context.Background()
	pool := newABSIdentityDatabase(t)

	userRepo := auth.NewUserRepository(pool)
	sessionRepo := auth.NewSessionRepository(pool)
	provider := pgstore.NewPostgresProvider(pool)
	authService := auth.NewService(
		auth.NewLocalProvider(userRepo, sessionRepo),
		auth.NewJWTService("abs-identity-contract-secret", time.Hour, 24*time.Hour),
		sessionRepo,
		userRepo,
		nil, nil, nil,
	)

	const (
		accountUsername = "casa-account"
		accountPassword = "fixture-account-password-001"
	)
	account, err := userRepo.Create(ctx, models.CreateUserInput{
		Email:    "casa-account@example.test",
		Username: accountUsername,
		Password: accountPassword,
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	tenancyStore := tenancy.NewStore(pool)
	if _, err := tenancyStore.ProvisionDefaultMembership(ctx, account.ID, "admin"); err != nil {
		t.Fatalf("provision membership: %v", err)
	}
	if _, err := tenancyStore.ActivateInitialOwnership(ctx, account.ID); err != nil {
		t.Fatalf("activate ownership: %v", err)
	}
	store, err := provider.ForUser(ctx, account.ID)
	if err != nil {
		t.Fatalf("open user store: %v", err)
	}
	for _, profile := range []userstore.Profile{
		{Name: "Primary-Casa"},
		{Name: "Reader-Nook"},
	} {
		if err := store.CreateProfile(ctx, profile); err != nil {
			t.Fatalf("create profile %s: %v", profile.Name, err)
		}
	}

	settingsRepo := catalog.NewServerSettingsRepo(pool)
	// The service-level settings reader gates only ABSCompatEnabled, which
	// this consumer does not exercise.
	service := New(nil)
	handler := service.BuildABSHandler(ABSHandlerDeps{
		Pool:     pool,
		Items:    catalog.NewItemRepository(pool),
		Files:    scanner.NewFileRepository(pool),
		Settings: settingsRepo,
		Auth: &SiloCredValidator{
			Auth: authService,
			Pool: pool,
		},
		// The group-policy provider requires the policy system's tenant
		// context; this consumer wires the legacy resolver over the real user
		// and profile rows, as proxy/test modes do.
		AccessResolver: NewABSAccessResolver(userRepo, provider,
			access.NewResolver(userRepo, provider, nil)),
	})
	server := httptest.NewServer(handler.Router())
	defer server.Close()

	bindings := map[string]string{
		"account_username":  accountUsername,
		"account_password":  accountPassword,
		"wrong_password":    "fixture-wrong-password-002",
		"account_id":        strconv.Itoa(account.ID),
		"primary_profile":   "Primary-Casa",
		"reader_profile":    "Reader-Nook",
		"unknown_profile":   "Ghost-Shelf",
		"missing_adult_id":  "missing-adult-item-001",
		"missing_random_id": "missing-random-item-002",
	}
	report, err := compatcontract.Run(ctx, compatcontract.Target{
		BaseURL:  server.URL,
		Client:   server.Client(),
		Bindings: bindings,
	}, compatcontract.AudiobookshelfIdentity())
	if err != nil {
		t.Fatalf("identity contract: %v; report=%s", err, report.JSON())
	}
}
