package jellycompat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatcontract"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

func newCompatIdentityDatabase(t *testing.T) *pgxpool.Pool {
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
	name := "jellycompat_identity_" + hex.EncodeToString(random[:])
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

// TestJellyfinIdentityContractAgainstRealRouter drives the frozen Jellyfin
// identity suite at the real embedded router — production NewRouter wiring
// with the real auth service, user store, login resolver, authenticator, and
// access-filter resolver over a disposable PostgreSQL database. Logins mint
// real compat sessions, PINs verify against the real store, and revocation
// happens through the real refresh gate after the underlying auth sessions
// are revoked.
func TestJellyfinIdentityContractAgainstRealRouter(t *testing.T) {
	ctx := context.Background()
	pool := newCompatIdentityDatabase(t)

	userRepo := auth.NewUserRepository(pool)
	sessionRepo := auth.NewSessionRepository(pool)
	provider := pgstore.NewPostgresProvider(pool)
	// AccessTokenExpiry sits inside the authenticator's 5-minute refresh
	// buffer so every authenticated compat request revalidates the underlying
	// Silo session — which is exactly the gate revocation flows through.
	authService := auth.NewService(
		auth.NewLocalProvider(userRepo, sessionRepo),
		auth.NewJWTService("jellyfin-identity-contract-secret", time.Minute, 24*time.Hour),
		sessionRepo,
		userRepo,
		nil, nil, nil,
	)

	const (
		accountUsername = "casa-account"
		accountPassword = "fixture-account-password-001"
		profilePIN      = "246810"
	)
	account, err := userRepo.Create(ctx, models.CreateUserInput{
		Email:    "casa-account@example.test",
		Username: accountUsername,
		Password: accountPassword,
		// The deployment's first account is the admin that owns the default
		// organization, exactly as real setup provisions it. The compat login
		// itself carries no admin semantics.
		Role: "admin",
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
	pinHash, err := bcrypt.GenerateFromPassword([]byte(profilePIN), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash pin: %v", err)
	}
	// The first profile becomes the household primary; naming it after the
	// account is the remembered/default selection the login resolver honors.
	for _, profile := range []userstore.Profile{
		{Name: accountUsername},
		{Name: "Reader-Nook"},
		{Name: "Vault-Keep", PINHash: string(pinHash)},
	} {
		if err := store.CreateProfile(ctx, profile); err != nil {
			t.Fatalf("create profile %s: %v", profile.Name, err)
		}
	}
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	profileIDs := map[string]string{}
	for _, profile := range profiles {
		profileIDs[profile.Name] = profile.ID
	}

	foreign, err := userRepo.Create(ctx, models.CreateUserInput{
		Email:    "casa-foreign@example.test",
		Username: "casa-foreign",
		Password: "fixture-foreign-password-005",
		Role:     "user",
	})
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	if _, err := tenancyStore.ProvisionDefaultMembership(ctx, foreign.ID, "user"); err != nil {
		t.Fatalf("provision foreign membership: %v", err)
	}
	foreignStore, err := provider.ForUser(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("open foreign store: %v", err)
	}
	if err := foreignStore.CreateProfile(ctx, userstore.Profile{Name: "Foreign-Family"}); err != nil {
		t.Fatalf("create foreign profile: %v", err)
	}
	foreignProfiles, err := foreignStore.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("list foreign profiles: %v", err)
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:          "jellyfin-identity-contract-secret",
			AccessTokenExpiry:  time.Minute,
			RefreshTokenExpiry: 24 * time.Hour,
		},
		JellyfinCompat: config.JellyfinCompatConfig{
			Enabled:    true,
			ServerID:   "fixture-jellyfin-server",
			SessionTTL: time.Hour,
		},
	}
	codec := NewResourceIDCodec()
	itemRepo := catalog.NewItemRepository(pool)
	seasonRepo := catalog.NewSeasonRepository(pool)
	episodeRepo := catalog.NewEpisodeRepository(pool)
	personRepo := catalog.NewPersonRepository(pool)
	router := NewRouter(Dependencies{
		Config:            cfg,
		AppContext:        ctx,
		DB:                pool,
		AuthService:       authService,
		UserStoreProvider: provider,
		// The in-memory session store keeps this consumer free of the at-rest
		// cipher; login, PIN, refresh, and revocation semantics are identical.
		SessionStore:   NewSessionStore(cfg.JellyfinCompat.SessionTTL, nil),
		IDCodec:        codec,
		BrowseRepo:     catalog.NewBrowseRepository(pool),
		ItemRepo:       itemRepo,
		SeasonRepo:     seasonRepo,
		EpisodeRepo:    episodeRepo,
		PersonRepo:     personRepo,
		DetailSvc:      catalog.NewDetailService(itemRepo, episodeRepo, seasonRepo, personRepo, nil),
		FolderRepo:     catalog.NewFolderRepository(pool),
		AccessFilterFn: NewScopeAccessFilter(access.NewResolver(userRepo, provider, nil)),
	})
	server := httptest.NewServer(router)
	defer server.Close()

	bindings := map[string]string{
		"account_username":  accountUsername,
		"account_password":  accountPassword,
		"wrong_password":    "fixture-wrong-password-002",
		"primary_profile":   accountUsername,
		"reader_profile":    "Reader-Nook",
		"pin_profile":       "Vault-Keep",
		"unknown_profile":   "Ghost-Shelf",
		"foreign_profile":   "Foreign-Family",
		"profile_pin":       profilePIN,
		"wrong_pin":         "135791",
		"sibling_pseudo_id": PseudoUserID(account.ID, profileIDs[accountUsername]).String(),
		"foreign_pseudo_id": PseudoUserID(foreign.ID, foreignProfiles[0].ID).String(),
		"missing_adult_id":  codec.EncodeStringID(EncodedIDItem, "missing-adult-item-001"),
		"missing_random_id": codec.EncodeStringID(EncodedIDItem, "missing-random-item-002"),
	}
	target := compatcontract.Target{BaseURL: server.URL, Client: server.Client(), Bindings: bindings}
	suite := compatcontract.JellyfinIdentity()

	// Phase 1: everything except the account-session revocation, which needs
	// real state to move between phases.
	var phaseOne []string
	for _, c := range suite.Cases {
		if c.Name != "a revoked account session is refused" {
			phaseOne = append(phaseOne, c.Name)
		}
	}
	report, err := compatcontract.Run(ctx, target, suite.Pick(phaseOne...))
	if err != nil {
		t.Fatalf("identity phase: %v; report=%s", err, report.JSON())
	}

	// Phase 2: revoke the account's underlying Silo sessions; the still-held
	// compat token dies at the authenticator's refresh gate.
	bindings["jf_revoked_token"] = bindings["jf_reader_token"]
	if err := sessionRepo.RevokeAllByUser(ctx, account.ID); err != nil {
		t.Fatalf("revoke account sessions: %v", err)
	}
	report, err = compatcontract.Run(ctx, target, suite.Pick("a revoked account session is refused"))
	if err != nil {
		t.Fatalf("revocation phase: %v; report=%s", err, report.JSON())
	}
}
