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
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
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
	// The attempt names a catalog file; its bytes are never on disk.
	var folderID, fileID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Claimless delivery') RETURNING id`).Scan(&folderID); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_files(content_id,media_folder_id,file_path,container,file_size) VALUES('claimless-movie',$1,'/invented/claimless/movie.mp4','mp4',4) RETURNING id`, folderID).Scan(&fileID); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	session, err := sessionMgr.StartSession(accountID, profileID, fileID, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	// startPlayback records the attempt with the login session that started
	// it; Silo's stream-reference check reads that record on every request.
	var loginSessionID string
	if err := pool.QueryRow(ctx, `SELECT id FROM auth_sessions WHERE user_id = $1 AND revoked_at IS NULL LIMIT 1`, accountID).Scan(&loginSessionID); err != nil {
		t.Fatalf("load login session: %v", err)
	}
	if err := planstore.NewPostgres(pool).SaveAttempt(ctx, playback.AttemptRecordV3{
		PlaybackAttemptID: "claimless-delivery", SessionID: session.ID, UserID: accountID, ProfileID: profileID,
		RequestedMediaFileID: fileID, EffectiveMediaFileID: fileID, CurrentPlanID: "plan-1",
		LoginSessionID: loginSessionID, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save attempt: %v", err)
	}

	// UserID/ProfileID/PlayMethod are the same recipe-card fields every real
	// stream_url token carries; RequireViewerAccess now uses them as lookup
	// keys to resolve a real, organization-bounded scope for this bearer-less
	// request instead of skipping scope resolution entirely.
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: session.ID, PlayMethod: string(playback.PlayDirect), UserID: accountID, ProfileID: profileID}, jwtSecret, time.Hour)
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

	// A stopped attempt ends its references with Silo's answer, which Silo's
	// apps read as "this playback is over", not as a missing file.
	if _, err := pool.Exec(ctx, `UPDATE playback_v3_attempts SET stopped_at = now() WHERE session_id = $1::uuid`, session.ID); err != nil {
		t.Fatalf("stop attempt: %v", err)
	}
	ended := performJSONRequest(t, router, http.MethodGet,
		"/api/v2/stream/"+session.ID+"?st="+token, "", "", nil)
	if ended.Code != http.StatusGone || !strings.Contains(ended.Body.String(), "/problems/playback_session_ended") {
		t.Fatalf("stopped attempt's reference = %d %s, want 410 playback_session_ended",
			ended.Code, ended.Body.String())
	}
}
