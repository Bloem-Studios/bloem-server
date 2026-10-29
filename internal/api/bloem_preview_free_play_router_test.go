package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/preview"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// A guest launched with a play allowance, through the whole router: the
// account says it may play and how much is left, a start gets past the
// preview gate while the direct stream stays shut, and when the allowance is
// used both close again.
func TestFreePlayGuestJourneyThroughTheRouter(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	admin, err := users.Create(ctx, models.CreateUserInput{Username: "free-play-admin", Email: "free-play-admin@example.test", Password: "synthetic-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := users.Create(ctx, models.CreateUserInput{Username: "free-play-shared", Email: "free-play-shared@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entitlements.NewTemplateStore(pool).ApplyDefaultAccountTemplate(ctx, admin.ID, shared.ID, accesspolicy.PreviewTemplateKey, 1, false); err != nil {
		t.Fatal(err)
	}
	profileID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,is_primary,organization_id,access_group_id) SELECT $1,u.id,'Preview',true,m.organization_id,u.access_group_id FROM users u JOIN organization_memberships m ON m.account_id=u.id WHERE u.id=$2 AND m.status='active'`, profileID, shared.ID); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := preview.NewStore(pool).Put(ctx, preview.Settings{Enabled: true, Host: "browse.example.test", Issuer: "garden-1", AccountID: shared.ID,
		TicketPublicKeys: []ed25519.PublicKey{pub}, SessionTTL: 2 * time.Hour, IdleTTL: 20 * time.Minute, MaxConcurrent: 200,
		Brand: "Example", StorefrontURL: "https://store.example.test/streaming", LaunchURL: "https://store.example.test/preview/launch",
		FreePlayEnabled: true, FreePlayURL: "https://store.example.test/preview/play"}); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{
		DB: pool, FolderRepo: catalog.NewFolderRepository(pool), FileRepo: scanner.NewFileRepository(pool),
		SessionMgr: playback.NewSessionManager(4, 2), UserStoreProvider: pgstore.NewPostgresProvider(pool),
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "free-play-router-test", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
	})
	serverID, err := bloemServerIdentity(Dependencies{DB: pool}).Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grant := "RouterRouterRouterRout"
	now := time.Now()
	ticket, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": "garden-1", "aud": serverID, "host": "browse.example.test",
		"jti": uuid.NewString()[:22], "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "item": "movie:1",
		"play": 1800, "grant": grant, "grant_exp": now.Add(24 * time.Hour).Unix()}).SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequest(method, path, &buf)
		r.Host = "browse.example.test"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Profile-Id", profileID)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := do("POST", "/api/bloem/v1/preview/sessions", "", map[string]string{"ticket": ticket})
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.AccessToken == "" {
		t.Fatalf("exchange: %d %s", w.Code, w.Body)
	}
	me := func() (bool, *preview.FreePlayView) {
		t.Helper()
		w := do("GET", "/api/v2/account/me", session.AccessToken, nil)
		var body struct {
			PlaybackAllowed *bool                 `json:"playback_allowed"`
			Preview         *preview.Presentation `json:"preview"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.PlaybackAllowed == nil || body.Preview == nil {
			t.Fatalf("account/me: %d %s", w.Code, w.Body)
		}
		return *body.PlaybackAllowed, body.Preview.FreePlay
	}
	if allowed, free := me(); !allowed || free == nil || free.AllowanceSeconds != 1800 {
		t.Fatalf("free-play account: allowed=%v free=%+v", allowed, free)
	}
	start := map[string]any{"file_id": uuid.NewString(), "client_playback_context": map[string]any{"deliveries": map[string]any{"hls": map[string]any{"enabled": true, "supported_on_device": true}}}}
	if w := do("POST", "/api/v2/playback/start", session.AccessToken, start); w.Header().Get("X-Bloem-Denial") == apimw.CodePreviewOnly {
		t.Fatalf("free-play start refused by the preview gate: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/v2/stream/"+uuid.NewString(), session.AccessToken, nil); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
		t.Fatalf("direct stream for free play: %d %s", w.Code, w.Body)
	}
	if _, err := pool.Exec(ctx, `UPDATE bloem_preview_play_grants SET used_seconds=allowance_seconds WHERE grant_id=$1`, grant); err != nil {
		t.Fatal(err)
	}
	if allowed, free := me(); allowed || free != nil {
		t.Fatalf("spent grant: allowed=%v free=%+v", allowed, free)
	}
	if w := do("POST", "/api/v2/playback/start", session.AccessToken, start); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
		t.Fatalf("start after the allowance: %d %s", w.Code, w.Body)
	}
}
