package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Playback start returns a stream_url carrying a signed session-bound token
// precisely because native players cannot attach a bearer to every range
// request. The token is the authorization: following the URL with no bearer
// must reach the stream handler, and a token for another session must not.
func TestStreamTokenAuthorizesClaimlessProgressiveDelivery(t *testing.T) {
	ctx := context.Background()
	pool := newV1TenancyDatabase(t)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	sessionMgr := playback.NewSessionManager(8, 4)
	const jwtSecret = "stream-token-delivery-secret"
	router := NewRouter(Dependencies{
		DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          jwtSecret,
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		SessionMgr:            sessionMgr,
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v2/auth/setup", `{
		"username":"owner","email":"owner@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	var accountID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&accountID); err != nil {
		t.Fatalf("load account: %v", err)
	}
	var profileID string
	if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND is_primary`, accountID).Scan(&profileID); err != nil {
		t.Fatalf("load profile: %v", err)
	}
	session, err := sessionMgr.StartSession(accountID, profileID, 1, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	// UserID/ProfileID are the same recipe-card fields every real stream_url
	// token carries; RequireViewerAccess now uses them as lookup keys to
	// resolve a real, organization-bounded scope for this bearer-less
	// request instead of skipping scope resolution entirely.
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: session.ID, UserID: accountID, ProfileID: profileID}, jwtSecret, time.Hour)
	if err != nil {
		t.Fatalf("sign stream token: %v", err)
	}

	// The fixture has no media file behind the session, so the handler's
	// answer for an authorized request is its own 404 — which proves the
	// request got past authentication to the handler's real logic.
	authorized := performJSONRequest(t, router, http.MethodGet,
		"/api/v2/stream/"+session.ID+"?st="+token, "", "", nil)
	if authorized.Code != http.StatusNotFound || !strings.Contains(authorized.Body.String(), "/problems/not_found") {
		t.Fatalf("tokened claimless delivery = %d %s, want the handler's own not-found",
			authorized.Code, authorized.Body.String())
	}

	// No token, no bearer: refused before the handler.
	bare := performJSONRequest(t, router, http.MethodGet, "/api/v2/stream/"+session.ID, "", "", nil)
	if bare.Code != http.StatusUnauthorized {
		t.Fatalf("claimless delivery without token = %d %s, want %d",
			bare.Code, bare.Body.String(), http.StatusUnauthorized)
	}

	// A token minted for another session must not authorize this one.
	other, err := streamtoken.Sign(streamtoken.Claims{SessionID: "another-session"}, jwtSecret, time.Hour)
	if err != nil {
		t.Fatalf("sign foreign token: %v", err)
	}
	crossed := performJSONRequest(t, router, http.MethodGet,
		"/api/v2/stream/"+session.ID+"?st="+other, "", "", nil)
	if crossed.Code != http.StatusUnauthorized {
		t.Fatalf("cross-session token = %d %s, want %d",
			crossed.Code, crossed.Body.String(), http.StatusUnauthorized)
	}
}
