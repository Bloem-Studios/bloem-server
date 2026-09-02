package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type multitenantAdminAcceptanceFixture struct {
	pool               *pgxpool.Pool
	router             http.Handler
	accountToken       string
	organizationIDs    [2]uuid.UUID
	membershipIDs      [2]uuid.UUID
	organizationTokens [2]string
	localMarkers       [2]string
	foreignMarkers     [2]string
}

// TestMultitenantAdminTwoOrganizationIsolation is the release-locking API
// acceptance for administrative contexts. Its database is created and dropped
// by newDisposableAPIDatabase, whose cleanup also verifies that the child
// database no longer exists.
func TestMultitenantAdminTwoOrganizationIsolation(t *testing.T) {
	fixture := newMultitenantAdminAcceptanceFixture(t)
	fixture.assertIsolationAndRevocation(t)
}

func newMultitenantAdminAcceptanceFixture(t *testing.T) *multitenantAdminAcceptanceFixture {
	t.Helper()
	ctx := context.Background()
	pool := newDisposableAPIDatabase(t, "bloem_multitenant_admin_", true)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write including the membership a new account is given.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}

	password := "correct horse battery staple"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash acceptance password: %v", err)
	}
	var bootstrapGroupID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE is_default ORDER BY id LIMIT 1`).Scan(&bootstrapGroupID); err != nil {
		t.Fatalf("load bootstrap access group: %v", err)
	}
	insertAccount := func(username string) int {
		var id int
		err := pool.QueryRow(ctx, `
			INSERT INTO users (username,email,password_hash,role,enabled)
			VALUES ($1,$2,$3,'user',true) RETURNING id`, username, username+"@example.test", passwordHash).Scan(&id)
		if err != nil {
			t.Fatalf("create account %q: %v", username, err)
		}
		return id
	}
	sharedAccountID := insertAccount("shared-admin")
	organizationOnly := [2]int{insertAccount("north-only"), insertAccount("canal-only")}

	fixture := &multitenantAdminAcceptanceFixture{pool: pool}
	fixture.localMarkers = [2]string{"NORTH-ONLY-MARKER", "CANAL-ONLY-MARKER"}
	fixture.foreignMarkers = [2]string{fixture.localMarkers[1], fixture.localMarkers[0]}
	organizationNames := [2]string{"North Sea Media", "Canal Media"}
	organizationSlugs := [2]string{"acceptance-north-sea", "acceptance-canal"}

	for index := range fixture.organizationIDs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO organizations (slug,name,status,owner_account_id)
			VALUES ($1,$2,'active',$3) RETURNING id`, organizationSlugs[index], organizationNames[index], sharedAccountID).Scan(&fixture.organizationIDs[index]); err != nil {
			t.Fatalf("create organization %d: %v", index, err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT $1,$2,'active','admin'
WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
ON CONFLICT (organization_id, account_id) DO UPDATE
SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role RETURNING id`, fixture.organizationIDs[index], sharedAccountID).Scan(&fixture.membershipIDs[index]); err != nil {
			t.Fatalf("create shared membership %d: %v", index, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_memberships (organization_id,account_id,status,legacy_role)
SELECT $1,$2,'active','user'
WHERE set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
ON CONFLICT (organization_id, account_id) DO UPDATE
SET status = EXCLUDED.status, legacy_role = EXCLUDED.legacy_role`, fixture.organizationIDs[index], organizationOnly[index]); err != nil {
			t.Fatalf("create organization-only membership %d: %v", index, err)
		}

		group, err := access.NewGroupStore(pool).Create(ctx, fixture.organizationIDs[index], access.CreateGroupInput{
			Name: "Group " + fixture.localMarkers[index], IsDefault: true,
		})
		if err != nil {
			t.Fatalf("create organization group %d: %v", index, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_profiles (user_id,id,name,is_primary,organization_id,access_group_id)
			VALUES ($1,$2,$3,true,$4,$5)`, organizationOnly[index], fmt.Sprintf("acceptance-profile-%d", index), "Profile "+fixture.localMarkers[index], fixture.organizationIDs[index], group.ID); err != nil {
			t.Fatalf("create organization profile %d: %v", index, err)
		}

		ownerID := opaAcceptanceOrganizationOwner(t, ctx, pool, fixture.organizationIDs[index])
		insertOPAAcceptanceFolder(t, ctx, pool, "Owned library "+fixture.localMarkers[index], &ownerID)
		var platformFolderID int64
		if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type,name) VALUES ('movies',$1) RETURNING id`, "Entitled library "+fixture.localMarkers[index]).Scan(&platformFolderID); err != nil {
			t.Fatalf("create platform library %d: %v", index, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_entitlements (
				organization_id,entitlement_kind,root_kind,root_owner_id,media_folder_id,status,granted_by_service
			) SELECT $1,'library_access','media_folder',owner_id,id,'active','multitenant-admin-acceptance'
			  FROM media_folders WHERE id=$2`, fixture.organizationIDs[index], platformFolderID); err != nil {
			t.Fatalf("create organization entitlement %d: %v", index, err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO policy_decisions (
				organization_id,membership_id,decision_name,policy_generation,user_id,profile_id,
				session_id,request_id,node_id,allowed,eval_time_ns,input_digest,input_sample,result_sample
			) VALUES ($1,$2,'playback.start',1,$3,$4,'acceptance','acceptance','acceptance',true,1,'digest',
				jsonb_build_object('action','playback.start','resource',jsonb_build_object('title',$5::text)),
				'{"allowed":true,"reason_code":"tenant_acceptance"}'::jsonb)`, fixture.organizationIDs[index], fixture.membershipIDs[index], sharedAccountID, fmt.Sprintf("acceptance-profile-%d", index), fixture.localMarkers[index]); err != nil {
			t.Fatalf("create policy decision %d: %v", index, err)
		}
	}

	cfg := &config.Config{Auth: config.AuthConfig{
		JWTSecret: "multitenant-admin-acceptance-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: 24 * time.Hour,
	}}
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	fixture.router = NewRouter(Dependencies{
		AppContext: ctx, DB: pool, Config: cfg, UserStoreProvider: pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap, MembershipProvisioner: bootstrap,
	})
	login := performJSONRequest(t, fixture.router, http.MethodPost, "/api/v1/auth/login", `{"username":"shared-admin","password":"`+password+`"}`, "", nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login shared administrator = %d %s", login.Code, login.Body.String())
	}
	fixture.accountToken = decodeLogin(t, login).AccessToken
	for index, organizationID := range fixture.organizationIDs {
		response := performJSONRequest(t, fixture.router, http.MethodPost, NativeAPIPrefix+"/admin/session", `{"scope":"organization","organization_id":"`+organizationID.String()+`"}`, fixture.accountToken, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("mint organization context %d = %d %s", index, response.Code, response.Body.String())
		}
		var session struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.AccessToken == "" {
			t.Fatalf("decode organization context %d: token=%q err=%v", index, session.AccessToken, err)
		}
		fixture.organizationTokens[index] = session.AccessToken
	}
	return fixture
}

func (fixture *multitenantAdminAcceptanceFixture) assertIsolationAndRevocation(t *testing.T) {
	t.Helper()
	paths := []string{
		NativeAPIPrefix + "/admin/organization/overview",
		NativeAPIPrefix + "/admin/organization/people/",
		NativeAPIPrefix + "/admin/organization/groups/",
		NativeAPIPrefix + "/admin/organization/libraries",
		NativeAPIPrefix + "/admin/organization/policy-decisions",
	}
	for index, token := range fixture.organizationTokens {
		for _, path := range paths {
			response := performJSONRequest(t, fixture.router, http.MethodGet, path, "", token, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("organization %d GET %s = %d %s", index, path, response.Code, response.Body.String())
			}
			body := response.Body.String()
			if path != NativeAPIPrefix+"/admin/organization/overview" && !strings.Contains(body, fixture.localMarkers[index]) {
				t.Errorf("organization %d GET %s omitted local marker: %s", index, path, body)
			}
			if strings.Contains(body, fixture.foreignMarkers[index]) {
				t.Errorf("organization %d GET %s leaked foreign marker: %s", index, path, body)
			}
		}
	}

	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE organization_memberships
		SET status='suspended',security_revision=security_revision+1,updated_at=now()
		WHERE id=$1`, fixture.membershipIDs[0]); err != nil {
		t.Fatalf("suspend first membership: %v", err)
	}
	revoked := performJSONRequest(t, fixture.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/overview", "", fixture.organizationTokens[0], nil)
	if revoked.Code != http.StatusForbidden || !strings.Contains(revoked.Body.String(), `"error":"organization_suspended"`) {
		t.Fatalf("already-minted suspended context = %d %s, want organization_suspended", revoked.Code, revoked.Body.String())
	}
	usable := performJSONRequest(t, fixture.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/overview", "", fixture.organizationTokens[1], nil)
	if usable.Code != http.StatusOK {
		t.Fatalf("unrelated organization context after suspension = %d %s", usable.Code, usable.Body.String())
	}
}
