package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

const (
	templateRevisionsDBPassword = "correct horse battery staple"
	familyRevisionsPath         = NativeAPIPrefix + "/admin/platform/entitlement-templates/family/revisions"
)

// templateRevisionsDB is the full router on a migrated database with a
// platform administrator, an organization administrator and a two-revision
// "family" template.
type templateRevisionsDB struct {
	router                         http.Handler
	organizationID                 string
	unscoped, accountsScoped, bulk string // the platform administrator's keys
	resellerKey                    string // the organization administrator's key
}

func newTemplateRevisionsDB(t *testing.T) *templateRevisionsDB {
	t.Helper()
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_template_revisions_key_", true)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(templateRevisionsDBPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	insertAccount := func(username, role string) int {
		var id int
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username,email,password_hash,role,enabled)
			VALUES ($1,$2,$3,$4,true) RETURNING id`, username, username+"@example.test", passwordHash, role).Scan(&id); err != nil {
			t.Fatalf("create account %q: %v", username, err)
		}
		return id
	}
	platformAdmin := insertAccount("platform-admin", "admin")
	// An organization administrator's admin role lives on the membership.
	organizationAdmin := insertAccount("reseller-admin", "user")
	fixture := &templateRevisionsDB{}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug,name,status,owner_account_id)
		VALUES ('revisions-reseller','Revisions Reseller','active',$1) RETURNING id`, organizationAdmin).Scan(&fixture.organizationID); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
		VALUES ($1,$2,'active','admin')
		ON CONFLICT (organization_id, account_id) DO UPDATE
		SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, fixture.organizationID, organizationAdmin); err != nil {
		t.Fatalf("create organization administrator membership: %v", err)
	}

	templates := entitlements.NewTemplateStore(pool)
	policy := entitlements.Policy{
		PlaybackAllowed: true, MaxStreams: 2, MaxProfiles: 3, TranscodeAllowed: true, MaxTranscodes: 1,
		DownloadAllowed: true, DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p", RequestsAllowed: true,
		AllowedPermissions: []string{"marker_edit"},
	}
	if _, err := templates.Create(ctx, entitlements.CreateTemplateInput{Key: "family", Name: "Family", Enabled: true, Policy: policy}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	policy.MaxStreams, policy.MaxPlaybackQuality = 4, "2160p"
	if _, err := templates.Revise(ctx, "family", 1, entitlements.ReviseTemplateInput{Name: "Family", Enabled: true, Policy: policy}); err != nil {
		t.Fatalf("revise template: %v", err)
	}

	keys := auth.NewAPIKeyRepository(pool)
	newKey := func(accountID int, label string, scopes []string) string {
		key, err := keys.Create(ctx, accountID, label, scopes)
		if err != nil || !strings.HasPrefix(key.Key, "sa_") {
			t.Fatalf("create API key %q: err=%v", label, err)
		}
		return key.Key
	}
	fixture.unscoped = newKey(platformAdmin, "storefront", nil)
	fixture.accountsScoped = newKey(platformAdmin, "storefront-accounts", []string{auth.ScopeAdminEntitlementsAccounts})
	fixture.bulk = newKey(platformAdmin, "bulk", []string{auth.ScopeAdminEntitlementsBulk})
	fixture.resellerKey = newKey(organizationAdmin, "reseller", nil)

	appCtx, stopApp := context.WithCancel(ctx)
	t.Cleanup(func() {
		stopApp()
		waitNoListeners(pool)
	})
	fixture.router = NewRouter(Dependencies{
		AppContext: appCtx, DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret: "template-revisions-api-key-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: v1TenancyBootstrap{store: tenancy.NewStore(pool)},
	})
	return fixture
}

func (f *templateRevisionsDB) request(t *testing.T, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return performJSONRequest(t, f.router, method, path, body, bearer, nil)
}

// adminContext logs username in and exchanges the login for an
// administrative context described by request.
func (f *templateRevisionsDB) adminContext(t *testing.T, username, request string) string {
	t.Helper()
	login := f.request(t, http.MethodPost, "/api/v2/auth/login", `{"username":"`+username+`","password":"`+templateRevisionsDBPassword+`"}`, "")
	wantTemplateStatus(t, "login "+username, login, http.StatusOK, "")
	response := f.request(t, http.MethodPost, NativeAPIPrefix+"/admin/session", request, decodeLogin(t, login).AccessToken)
	wantTemplateStatus(t, "administrative context "+request, response, http.StatusOK, "")
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.AccessToken == "" {
		t.Fatalf("decode administrative context: err=%v", err)
	}
	return session.AccessToken
}

// wantFamilyRevisions checks both stored revisions, newest first, with the
// older revision's limits intact.
func wantFamilyRevisions(t *testing.T, label string, response *httptest.ResponseRecorder) {
	t.Helper()
	wantTemplateStatus(t, label, response, http.StatusOK, "")
	var body struct {
		Revisions []struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
			Policy   struct {
				MaxStreams         int    `json:"max_streams"`
				MaxPlaybackQuality string `json:"max_playback_quality"`
			} `json:"policy"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode revisions: %v", label, err)
	}
	if len(body.Revisions) != 2 || body.Revisions[0].Revision != 2 || body.Revisions[1].Revision != 1 ||
		body.Revisions[1].Key != "family" || body.Revisions[1].Policy.MaxStreams != 2 || body.Revisions[1].Policy.MaxPlaybackQuality != "1080p" {
		t.Fatalf("%s: revisions = %s", label, response.Body.String())
	}
}

// TestBloemTemplateRevisionsPlatformAPIKeyEndToEnd runs the full router on a
// migrated database: a platform administrator's API key reads a template's
// revisions, unscoped or with admin:entitlements:accounts, while a key or an
// organization context of an organization administrator is refused, and the
// platform context keeps reading and revising.
func TestBloemTemplateRevisionsPlatformAPIKeyEndToEnd(t *testing.T) {
	f := newTemplateRevisionsDB(t)
	for label, key := range map[string]string{"unscoped key": f.unscoped, "accounts-scoped key": f.accountsScoped} {
		wantFamilyRevisions(t, label+" GET revisions", f.request(t, http.MethodGet, familyRevisionsPath, "", key))
		wantTemplateStatus(t, label+" single template read",
			f.request(t, http.MethodGet, NativeAPIPrefix+"/admin/platform/entitlement-templates/family", "", key), http.StatusUnauthorized, "")
		wantTemplateStatus(t, label+" new revision",
			f.request(t, http.MethodPost, familyRevisionsPath, `{"expected_revision":2,"name":"Family","enabled":true,"policy":{}}`, key), http.StatusUnauthorized, "")
	}
	wantTemplateStatus(t, "bulk-scoped key GET revisions", f.request(t, http.MethodGet, familyRevisionsPath, "", f.bulk), http.StatusForbidden, "forbidden")
	wantTemplateStatus(t, "organization administrator's key GET revisions",
		f.request(t, http.MethodGet, familyRevisionsPath, "", f.resellerKey), http.StatusForbidden, "insufficient_platform_authority")

	organizationContext := f.adminContext(t, "reseller-admin", `{"scope":"organization","organization_id":"`+f.organizationID+`"}`)
	wantTemplateStatus(t, "organization context GET revisions",
		f.request(t, http.MethodGet, familyRevisionsPath, "", organizationContext), http.StatusForbidden, "insufficient_platform_authority")

	platformContext := f.adminContext(t, "platform-admin", `{"scope":"platform"}`)
	wantFamilyRevisions(t, "platform context GET revisions", f.request(t, http.MethodGet, familyRevisionsPath, "", platformContext))
	wantFamilyRevisions(t, "platform context GET history",
		f.request(t, http.MethodGet, NativeAPIPrefix+"/admin/platform/entitlement-templates/family/history", "", platformContext))
	wantTemplateStatus(t, "platform context single revision read",
		f.request(t, http.MethodGet, NativeAPIPrefix+"/admin/platform/entitlement-templates/family?revision=1", "", platformContext), http.StatusOK, "")
	wantTemplateStatus(t, "platform context new revision",
		f.request(t, http.MethodPost, familyRevisionsPath, `{"expected_revision":2,"source_revision":1,"name":"Family","enabled":true}`, platformContext), http.StatusCreated, "")
}
