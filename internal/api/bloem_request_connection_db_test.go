package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

// connectionRouterSpy stands in for the request_router plugin: Validate
// accepts everything, and TestConnection records the connection it was given.
type connectionRouterSpy struct {
	requests.RequestRouterProvider
	tests   []requests.ResolvedRouterConnection
	ok      bool
	message string
}

func (r *connectionRouterSpy) Validate(context.Context, int, string, requests.ResolvedRouterConnection, []requests.ResolvedRouterConnection) (map[string]string, string, error) {
	return nil, "", nil
}

func (r *connectionRouterSpy) TestConnection(_ context.Context, _ int, _ string, conn requests.ResolvedRouterConnection) (bool, string, error) {
	r.tests = append(r.tests, conn)
	return r.ok, r.message, nil
}

type requestConnectionEnv struct {
	pool   *pgxpool.Pool
	admin  bookrequests.Viewer
	router *connectionRouterSpy
	store  *requests.Repository
	books  *bookrequests.Service
	conns  *requests.Service
}

func newRequestConnectionEnv(t *testing.T) *requestConnectionEnv {
	t.Helper()
	pool := newDisposableAPIDatabase(t, "bloem_request_connection_test_", true)
	if err := database.RunMigrations(t.Context(), pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	cipher, err := secret.New([]byte("synthetic-request-connection-key!!"))
	if err != nil {
		t.Fatal(err)
	}
	env := &requestConnectionEnv{pool: pool, router: &connectionRouterSpy{}, store: requests.NewRepository(pool, cipher), books: bookrequests.NewService(pool, nil)}
	env.admin.Admin = true
	if err := pool.QueryRow(t.Context(), `SELECT id FROM organizations WHERE is_default`).Scan(&env.admin.OrganizationID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role) VALUES('lane-admin','lane-admin@example.test','x','admin') RETURNING id`).Scan(&env.admin.UserID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if err = tenancy.MarkMembershipPolicyWriter(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','admin') ON CONFLICT DO NOTHING`, env.admin.OrganizationID, env.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	env.conns = requests.NewService(env.store, nil, nil)
	env.conns.SetTenantScopeResolver(tenancy.NewStore(pool))
	env.conns.SetRouterProvider(env.router)
	return env
}

func (e *requestConnectionEnv) installation(t *testing.T, plugin, capability string) int {
	t.Helper()
	var owner uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'1','/fixture',$2) RETURNING id`, plugin, owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1',$2)`, id, capability); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *requestConnectionEnv) platform() auth.PlatformAdminAuthorizer {
	return auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(e.pool))
}

func TestAudiobookConnectionEditRotatesWithoutRebindingSavedSecretsDatabase(t *testing.T) {
	env := newRequestConnectionEnv(t)
	install := env.installation(t, bookrequests.AudiobookRequestPluginID, "audiobooks")
	s := bloemAudiobookSetup{pool: env.pool, books: env.books, requests: env.conns, platform: env.platform(), store: env.store, router: env.router}
	ctx := t.Context()
	config := map[string]any{"qbittorrent_url": "https://client.example.test/qb", "qbittorrent_username": "reader"}
	created, err := s.Create(ctx, env.admin, handlers.AudiobookRequestConnectionInput{Name: "Audiobooks", InstallationID: install, CapabilityID: "audiobooks", PluginConfig: config, Credentials: map[string]string{"qbittorrent_password": "saved-pass"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := env.store.GetIntegration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	setup, err := s.List(ctx, env.admin, true)
	if err != nil || len(setup.Integrations) != 1 {
		t.Fatalf("setup %+v %v", setup, err)
	}
	listed := setup.Integrations[0]
	body, _ := json.Marshal(setup)
	if !slices.Equal(listed.StoredCredentials, []string{"qbittorrent_password"}) || listed.PluginConfig["qbittorrent_url"] != "https://client.example.test/qb" || strings.Contains(string(body), "saved-pass") || strings.Contains(string(body), "abk1.") {
		t.Fatalf("setup listing exposes or omits editor state: %s", body)
	}
	// Without an editor context the listing carries identity and binding only.
	if setup, err = s.List(ctx, env.admin, false); err != nil || len(setup.Integrations) != 1 {
		t.Fatalf("setup %+v %v", setup, err)
	}
	if body, _ = json.Marshal(setup); strings.Contains(string(body), "plugin_config") || strings.Contains(string(body), "stored_credentials") {
		t.Fatalf("non-editor listing exposes editor state: %s", body)
	}

	// Silo's generic editor cannot repoint the saved password.
	generic := requests.NewService(env.store, nil, nil)
	generic.SetRouterProvider(env.router)
	redirected := *before
	redirected.APIKeyRef = ""
	redirected.PluginConfig = map[string]any{"qbittorrent_url": "https://attacker.example.test/qb", "qbittorrent_username": "reader"}
	var validation *requests.ValidationError
	if _, err = generic.UpdateIntegration(ctx, requests.Viewer{UserID: env.admin.UserID, IsAdmin: true}, redirected); !errors.As(err, &validation) {
		t.Fatalf("generic edit of audiobook connection: %v", err)
	}
	if _, err = generic.UpdateIntegrationConditional(ctx, requests.Viewer{UserID: env.admin.UserID, IsAdmin: true}, redirected, before.Revision); !errors.As(err, &validation) {
		t.Fatalf("generic v2 edit of audiobook connection: %v", err)
	}

	// Bloem's editor refuses the same move until the password is re-entered.
	if _, err = s.Update(ctx, env.admin, created.ID, handlers.AudiobookRequestConnectionUpdate{PluginConfig: redirected.PluginConfig}); !errors.Is(err, bookrequests.ErrInvalid) {
		t.Fatalf("saved secret followed a changed URL: %v", err)
	}
	unchanged, err := env.store.GetIntegration(ctx, created.ID)
	if err != nil || unchanged.APIKeyRef != before.APIKeyRef || unchanged.Revision != before.Revision {
		t.Fatalf("refused edits changed the row: %+v %v", unchanged, err)
	}

	// Rotation in place: same id, same URL, new password, name change.
	updated, err := s.Update(ctx, env.admin, created.ID, handlers.AudiobookRequestConnectionUpdate{Name: "Rotated", Credentials: map[string]string{"qbittorrent_password": "rotated-pass"}})
	if err != nil || updated.ID != created.ID || updated.Name != "Rotated" {
		t.Fatalf("rotation %+v %v", updated, err)
	}
	rotated, err := env.store.GetIntegration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	secrets, bind, err := decodeAudiobookCredentials(rotated.APIKeyRef)
	if err != nil || secrets["qbittorrent_password"] != "rotated-pass" || bind["qbittorrent_url"] != audiobookURLFingerprint("https://client.example.test/qb") || !slices.Equal(rotated.SupportedMediaTypes, []string{"audiobook"}) || rotated.BaseURL != "" {
		t.Fatalf("rotated row %+v %v", rotated, err)
	}

	// A move with the password re-entered binds it to the new URL only.
	if _, err = s.Update(ctx, env.admin, created.ID, handlers.AudiobookRequestConnectionUpdate{PluginConfig: redirected.PluginConfig, Credentials: map[string]string{"qbittorrent_password": "new-host-pass"}}); err != nil {
		t.Fatal(err)
	}
	moved, err := env.store.GetIntegration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secrets, bind, err = decodeAudiobookCredentials(moved.APIKeyRef); err != nil || secrets["qbittorrent_password"] != "new-host-pass" || bind["qbittorrent_url"] != audiobookURLFingerprint("https://attacker.example.test/qb") {
		t.Fatal("re-entered password not bound to its new URL")
	}

	// Test sends the saved connection to the plugin and withholds an echo.
	env.router.ok, env.router.message = false, "login failed for new-host-pass"
	result, err := s.Test(ctx, env.admin, created.ID)
	if err != nil || result.OK || strings.Contains(result.Message, "new-host-pass") || len(env.router.tests) != 1 || env.router.tests[0].APIKey != moved.APIKeyRef || env.router.tests[0].Config["qbittorrent_url"] != "https://attacker.example.test/qb" {
		t.Fatalf("connection test %+v %v %+v", result, err, env.router.tests)
	}
	// An echo of the saved composite is withheld too: it decodes to the password.
	env.router.message = "unreadable credentials " + moved.APIKeyRef
	if result, err = s.Test(ctx, env.admin, created.ID); err != nil || strings.Contains(result.Message, "abk1.") || len(env.router.tests) != 2 {
		t.Fatalf("composite echo shown: %+v %v", result, err)
	}

	// A binding that no longer matches its URL is refused before the plugin.
	if _, err = env.pool.Exec(ctx, `UPDATE request_integrations SET plugin_config=jsonb_set(plugin_config,'{qbittorrent_url}','"https://elsewhere.example.test"') WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	result, err = s.Test(ctx, env.admin, created.ID)
	if err != nil || result.OK || len(env.router.tests) != 2 {
		t.Fatalf("mismatched binding reached the plugin: %+v %v", result, err)
	}
	// Dispatch reads the same saved row and refuses before resolving a plugin.
	client := &audiobookRouterClientFixture{}
	dispatch := bloemAudiobookRouter{integrations: env.store, resolver: ebookResolverFixture{client}, validator: audiobookValidatorFixture{}}
	work := bookrequests.AudiobookDispatchInput{Request: bookrequests.Request{ID: "request", MediaType: "audiobook", Title: "Synthetic Saga"}, Integration: bookrequests.AudiobookIntegration{ID: created.ID, InstallationID: install, CapabilityID: "audiobooks"}, SubmissionID: "12345678-1234-1234-1234-123456789abc", Attempt: 1}
	if _, err = dispatch.Fulfill(ctx, work); !errors.Is(err, bookrequests.ErrProviderNotCalled) || !errors.Is(err, bookrequests.ErrConflict) || client.input != nil {
		t.Fatalf("dispatch sent a credential to a moved URL: %v", err)
	}

	if _, err = s.Test(ctx, env.admin, "missing"); !errors.Is(err, bookrequests.ErrNotFound) {
		t.Fatalf("unknown connection: %v", err)
	}
	if _, err = env.pool.Exec(ctx, `UPDATE users SET role='user' WHERE id=$1`, env.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Test(ctx, env.admin, created.ID); !errors.Is(err, bookrequests.ErrForbidden) {
		t.Fatalf("test without platform authority: %v", err)
	}
	if _, err = s.Update(ctx, env.admin, created.ID, handlers.AudiobookRequestConnectionUpdate{Name: "x"}); !errors.Is(err, bookrequests.ErrForbidden) {
		t.Fatalf("update without platform authority: %v", err)
	}
	// An organization administrator without platform authority reads the
	// binding view but not the saved settings, even when it asks for them.
	setup, err = s.List(ctx, env.admin, true)
	if err != nil || len(setup.Integrations) != 1 || setup.Integrations[0].ID != created.ID {
		t.Fatalf("organization administrator listing %+v %v", setup, err)
	}
	if body, _ = json.Marshal(setup); strings.Contains(string(body), "plugin_config") || strings.Contains(string(body), "stored_credentials") || strings.Contains(string(body), "example.test") {
		t.Fatalf("organization administrator saw editor state: %s", body)
	}
}

func TestEbookConnectionTestUsesSavedConnectionDatabase(t *testing.T) {
	env := newRequestConnectionEnv(t)
	install := env.installation(t, "bloem.requests.bookwarehouse", "bookwarehouse")
	s := bloemEbookSetup{pool: env.pool, books: env.books, requests: env.conns, platform: env.platform(), store: env.store, router: env.router}
	ctx := t.Context()
	created, err := s.Create(ctx, env.admin, handlers.EbookRequestConnectionInput{Name: "Books", BaseURL: "https://books.example.test", APIKey: "synthetic-book-key", InstallationID: install, CapabilityID: "bookwarehouse"})
	if err != nil {
		t.Fatal(err)
	}
	env.router.ok, env.router.message = true, "Bookwarehouse monitoring API authenticated."
	result, err := s.Test(ctx, env.admin, created.ID)
	if err != nil || !result.OK || result.Message != "Bookwarehouse monitoring API authenticated." || len(env.router.tests) != 1 || env.router.tests[0].APIKey != "synthetic-book-key" || env.router.tests[0].BaseURL != "https://books.example.test" {
		t.Fatalf("ebook connection test %+v %v %+v", result, err, env.router.tests)
	}
	// An audiobook connection is not an ebook connection.
	audiobook := env.installation(t, bookrequests.AudiobookRequestPluginID, "audiobooks")
	audio := bloemAudiobookSetup{pool: env.pool, books: env.books, requests: env.conns, platform: env.platform(), store: env.store}
	other, err := audio.Create(ctx, env.admin, handlers.AudiobookRequestConnectionInput{Name: "Audio", InstallationID: audiobook, CapabilityID: "audiobooks", PluginConfig: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	// An empty saved configuration is listed as {}, not omitted: absence
	// means the saved settings could not be read.
	setup, err := audio.List(ctx, env.admin, true)
	if body, _ := json.Marshal(setup); err != nil || !strings.Contains(string(body), `"plugin_config":{}`) {
		t.Fatalf("empty saved configuration not listed: %s %v", body, err)
	}
	if _, err = s.Test(ctx, env.admin, other.ID); !errors.Is(err, bookrequests.ErrNotFound) {
		t.Fatalf("ebook test reached an audiobook connection: %v", err)
	}
}
