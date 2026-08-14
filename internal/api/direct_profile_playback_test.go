package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// A playback session id is a bearer for progress, stop, control, and media
// delivery. Two profiles on one account share a user_id, so an account-level
// ownership check cannot tell them apart — which was fine until a session could
// authenticate one profile rather than the whole household.
func TestDirectProfileSessionCannotTouchSiblingPlaybackSession(t *testing.T) {
	ctx := context.Background()
	pool := newV1TenancyDatabase(t)
	bootstrap := v1TenancyBootstrap{store: tenancy.NewStore(pool)}
	sessionMgr := playback.NewSessionManager(8, 4)
	router := NewRouter(Dependencies{
		DB: pool,
		Config: &config.Config{Auth: config.AuthConfig{
			JWTSecret:          "direct-profile-playback-secret",
			AccessTokenExpiry:  time.Hour,
			RefreshTokenExpiry: 24 * time.Hour,
		}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
		SessionMgr:            sessionMgr,
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
		"username":"owner","email":"owner@example.test","password":"correct horse battery staple",
		"create_default_profile":true,"default_profile_name":"Owner"
	}`, "", nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	accountToken := decodeLogin(t, setup).AccessToken
	var accountID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = 'owner'`).Scan(&accountID); err != nil {
		t.Fatalf("load account: %v", err)
	}

	credentials := auth.NewProfileCredentialService(pool)
	login := func(name, email string) (string, string) {
		t.Helper()
		if created := performJSONRequest(t, router, http.MethodPost, "/api/v1/profiles/",
			`{"name":"`+name+`"}`, accountToken, nil); created.Code >= http.StatusBadRequest {
			t.Fatalf("create %s: %d %s", name, created.Code, created.Body.String())
		}
		var profileID string
		if err := pool.QueryRow(ctx, `SELECT id FROM user_profiles WHERE user_id = $1 AND name = $2`,
			accountID, name).Scan(&profileID); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if err := credentials.Set(ctx, accountID, profileID, email, "profile-password"); err != nil {
			t.Fatalf("set %s credential: %v", name, err)
		}
		response := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/profile-login",
			`{"email":"`+email+`","password":"profile-password","device_id":"`+name+`-device"}`, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s login = %d %s", name, response.Code, response.Body.String())
		}
		return profileID, decodeLogin(t, response).AccessToken
	}

	readerProfileID, readerToken := login("Reader", "reader@example.test")
	siblingProfileID, _ := login("Sibling", "sibling@example.test")

	mine, err := sessionMgr.StartSession(accountID, readerProfileID, 1, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start own session: %v", err)
	}
	theirs, err := sessionMgr.StartSession(accountID, siblingProfileID, 2, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start sibling session: %v", err)
	}
	if mine.UserID != theirs.UserID {
		t.Fatalf("fixture is not exercising the shared-account case: %d vs %d", mine.UserID, theirs.UserID)
	}

	// Replan is absent deliberately: it already compares the profile as well as
	// the account, so it was never part of this hole.
	for name, probe := range map[string]struct{ method, path, body string }{
		"progress":       {http.MethodPost, "/api/v1/playback/" + theirs.ID + "/progress", `{"position":42}`},
		"stop":           {http.MethodDelete, "/api/v1/playback/" + theirs.ID, ""},
		"control":        {http.MethodGet, "/api/v1/playback/sessions/" + theirs.ID + "/control/ws", ""},
		"stream":         {http.MethodGet, "/api/v1/stream/" + theirs.ID, ""},
		"stream head":    {http.MethodHead, "/api/v1/stream/" + theirs.ID, ""},
		"subtitles":      {http.MethodGet, "/api/v1/stream/" + theirs.ID + "/subtitles/0", ""},
		"subtitles head": {http.MethodHead, "/api/v1/stream/" + theirs.ID + "/subtitles/0", ""},
		"transcode":      {http.MethodGet, "/api/v1/playback/transcode/" + theirs.ID + "/master.m3u8", ""},
	} {
		t.Run("sibling "+name+" is refused", func(t *testing.T) {
			response := performJSONRequest(t, router, probe.method, probe.path, probe.body, readerToken, nil)
			if response.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d %s, want %d",
					probe.method, probe.path, response.Code, response.Body.String(), http.StatusForbidden)
			}
		})
	}

	// The sibling's session is still running: a refused stop must not have
	// stopped it.
	if _, err := sessionMgr.GetSession(theirs.ID); err != nil {
		t.Fatalf("sibling session was destroyed by a refused request: %v", err)
	}

	t.Run("own session takes the progress", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodPost,
			"/api/v1/playback/"+mine.ID+"/progress", `{"position":42}`, readerToken, nil)
		if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
			t.Fatalf("own session progress = %d %s, want the bound profile to control its own playback",
				response.Code, response.Body.String())
		}
		// The refusals above are only meaningful if the permitted call did
		// something: assert the position actually moved.
		updated, err := sessionMgr.GetSession(mine.ID)
		if err != nil {
			t.Fatalf("reload own session: %v", err)
		}
		if updated.Position < 42 {
			t.Fatalf("own session position = %v, want the reported progress to have landed", updated.Position)
		}
	})

	// Runs after the progress assertions: a HEAD probe that reaches the
	// handler aborts a session whose media file is gone, so this destroys
	// `mine` and must come last.
	//
	// Players probe with HEAD before they fetch; the boundary must let the
	// probe through to the handler for the session's own profile. The fixture
	// has no media file behind the session, so the handler's own not-found is
	// the proof the request got past the boundary.
	t.Run("own session HEAD probe reaches the handler", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodHead, "/api/v1/stream/"+mine.ID, "", readerToken, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("own HEAD probe = %d %s, want the handler's not-found rather than a boundary refusal",
				response.Code, response.Body.String())
		}
	})

	// Proxy-served direct downloads carry the same profile-scoped
	// authorization as the tokened route; the boundary must admit them. A
	// request with no file_id is answered by the handler's own validation,
	// which is the proof it was not refused at the boundary.
	t.Run("direct-download-proxy reaches the handler", func(t *testing.T) {
		response := performJSONRequest(t, router, http.MethodGet, "/api/v1/direct-download-proxy", "", readerToken, nil)
		if response.Code != http.StatusServiceUnavailable && response.Code != http.StatusBadRequest {
			t.Fatalf("direct-download-proxy = %d %s, want the handler's own answer rather than a boundary refusal",
				response.Code, response.Body.String())
		}
	})
}

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
		MembershipProvisioner: bootstrap,
		SessionMgr:            sessionMgr,
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
	})

	setup := performJSONRequest(t, router, http.MethodPost, "/api/v1/auth/setup", `{
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

	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: session.ID}, jwtSecret, time.Hour)
	if err != nil {
		t.Fatalf("sign stream token: %v", err)
	}

	// The fixture has no media file behind the session, so the handler's
	// answer for an authorized request is its own 404 — which proves the
	// request got past authentication to the handler's real logic.
	authorized := performJSONRequest(t, router, http.MethodGet,
		"/api/v1/stream/"+session.ID+"?st="+token, "", "", nil)
	if authorized.Code != http.StatusNotFound || !strings.Contains(authorized.Body.String(), "Media file not found") {
		t.Fatalf("tokened claimless delivery = %d %s, want the handler's own not-found",
			authorized.Code, authorized.Body.String())
	}

	// No token, no bearer: refused before the handler.
	bare := performJSONRequest(t, router, http.MethodGet, "/api/v1/stream/"+session.ID, "", "", nil)
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
		"/api/v1/stream/"+session.ID+"?st="+other, "", "", nil)
	if crossed.Code != http.StatusUnauthorized {
		t.Fatalf("cross-session token = %d %s, want %d",
			crossed.Code, crossed.Body.String(), http.StatusUnauthorized)
	}
}
