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

func TestPreviewGuestJourneyThroughTheRouter(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	admin, err := users.Create(ctx, models.CreateUserInput{Username: "guest-admin", Email: "guest-admin@example.test", Password: "synthetic-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := users.Create(ctx, models.CreateUserInput{Username: "preview-shared", Email: "preview-shared@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entitlements.NewTemplateStore(pool).ApplyDefaultAccountTemplate(ctx, admin.ID, shared.ID, accesspolicy.PreviewTemplateKey, 1, false); err != nil {
		t.Fatal(err)
	}
	profileID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id,user_id,name,is_primary,organization_id,access_group_id) SELECT $1,$2,'Preview',true,organization_id,access_group_id FROM organization_memberships WHERE account_id=$2 AND status='active'`, profileID, shared.ID); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	store := preview.NewStore(pool)
	if err := store.Put(ctx, preview.Settings{Enabled: true, Host: "browse.example.test", Issuer: "garden-1", AccountID: shared.ID,
		TicketPublicKeys: []ed25519.PublicKey{pub}, SessionTTL: 2 * time.Hour, IdleTTL: 20 * time.Minute, MaxConcurrent: 200,
		Brand: "Example", StorefrontURL: "https://store.example.test/streaming", LaunchURL: "https://store.example.test/preview/launch"}); err != nil {
		t.Fatal(err)
	}
	const secret = "guest-router-test-secret"
	router := NewRouter(Dependencies{
		DB: pool, FolderRepo: catalog.NewFolderRepository(pool), FileRepo: scanner.NewFileRepository(pool),
		SessionMgr: playback.NewSessionManager(4, 2), UserStoreProvider: pgstore.NewPostgresProvider(pool),
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: secret, AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
	})
	serverID, err := bloemServerIdentity(Dependencies{DB: pool}).Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticket := func() string {
		now := time.Now()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": "garden-1", "aud": serverID, "host": "browse.example.test",
			"jti": uuid.NewString()[:22], "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "item": "movie:1", "ref": "signed-affiliate"}).SignedString(priv)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	do := func(host, method, path, bearer string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequest(method, path, &buf)
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Profile-Id", profileID)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Exchange works only on the preview host.
	if w := do("lib.example.test", "POST", "/api/bloem/v1/preview/sessions", "", map[string]string{"ticket": ticket()}); w.Code != http.StatusNotFound {
		t.Fatalf("exchange on the normal host: %d %s", w.Code, w.Body)
	}
	w := do("browse.example.test", "POST", "/api/bloem/v1/preview/sessions", "", map[string]string{"ticket": ticket()})
	if w.Code != http.StatusCreated {
		t.Fatalf("exchange: %d %s", w.Code, w.Body)
	}
	var session struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Item         string `json:"item"`
		Ref          string `json:"ref"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.AccessToken == "" || session.Item != "movie:1" || session.Ref != "signed-affiliate" {
		t.Fatalf("exchange body %s (%v)", w.Body, err)
	}

	// Browse on the preview host; refused everywhere else.
	if w := do("browse.example.test", "GET", "/api/v2/account/me", session.AccessToken, nil); w.Code != http.StatusOK {
		t.Fatalf("guest account/me: %d %s", w.Code, w.Body)
	}
	meResponse := do("browse.example.test", "GET", "/api/v2/account/me", session.AccessToken, nil)
	var me struct {
		Email           string                `json:"email"`
		Permissions     []string              `json:"permissions"`
		DownloadAllowed bool                  `json:"download_allowed"`
		PlaybackAllowed *bool                 `json:"playback_allowed"`
		Preview         *preview.Presentation `json:"preview"`
	}
	if err := json.Unmarshal(meResponse.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Preview == nil || me.Preview.Kind != "guest" || me.Preview.EndsAt == nil || me.PlaybackAllowed == nil || *me.PlaybackAllowed {
		t.Fatalf("missing guest presentation: %s", meResponse.Body)
	}
	var absolute time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE auth_method='preview_guest' ORDER BY created_at DESC LIMIT 1`).Scan(&absolute); err != nil || !absolute.Equal(*me.Preview.EndsAt) {
		t.Fatal("guest end date is not absolute session expiry")
	}
	capability := do("browse.example.test", "GET", "/api/v2/playback/capabilities", session.AccessToken, nil)
	var caps struct {
		Allowed *bool `json:"allowed"`
		Denial  struct {
			Code     string `json:"code"`
			CTALabel string `json:"cta_label"`
		} `json:"denial"`
	}
	if err := json.Unmarshal(capability.Body.Bytes(), &caps); err != nil || capability.Code != 200 || caps.Allowed == nil || *caps.Allowed || caps.Denial.Code != "preview_only" || caps.Denial.CTALabel != "Subscribe to Example" {
		t.Fatalf("guest capabilities: %d %s", capability.Code, capability.Body)
	}
	if w := do("browse.example.test", "POST", "/api/bloem/v1/preview/events", session.AccessToken, map[string]string{"event": "cta_shown"}); w.Code != 204 {
		t.Fatalf("guest event: %d %s", w.Code, w.Body)
	}
	if me.Email != "" || len(me.Permissions) != 0 || me.DownloadAllowed {
		t.Fatalf("guest account/me is not minimized: %+v", me)
	}
	if w := do("browse.example.test", "POST", "/api/v2/events/ws-ticket", session.AccessToken, map[string]string{}); w.Code != http.StatusForbidden {
		t.Fatalf("guest audience ticket mint: %d", w.Code)
	}

	if w := do("browse.example.test", "GET", "/api/v2/user/libraries", session.AccessToken, nil); w.Code != http.StatusOK {
		t.Fatalf("guest libraries: %d %s", w.Code, w.Body)
	}
	if w := do("lib.example.test", "GET", "/api/v2/user/libraries", session.AccessToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("guest token on the normal host: %d", w.Code)
	}
	// The normal host claiming the preview host through X-Forwarded-Host is
	// still the normal host.
	spoof := httptest.NewRequest("GET", "/api/v2/user/libraries", nil)
	spoof.Host = "lib.example.test"
	spoof.Header.Set("X-Forwarded-Host", "browse.example.test")
	spoof.Header.Set("X-Profile-Id", profileID)
	spoof.Header.Set("Authorization", "Bearer "+session.AccessToken)
	spoofed := httptest.NewRecorder()
	router.ServeHTTP(spoofed, spoof)
	if spoofed.Code != http.StatusUnauthorized {
		t.Fatalf("guest token with a spoofed X-Forwarded-Host: %d", spoofed.Code)
	}
	// Rule set C: no playback. Rule set G: the account surface lives under
	// /api/v2/auth, which the preview host refuses outright (the v2 gate's
	// guest refusal behind it is Task 7's unit test).
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v2/auth/sessions"},
		{"POST", "/api/v2/auth/device/approve"},
		{"POST", "/api/v2/playback/start"},
	} {
		if w := do("browse.example.test", tc.method, tc.path, session.AccessToken, map[string]string{}); w.Code != http.StatusForbidden {
			t.Errorf("guest %s %s = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
	// Other sign-ins are refused on the preview host.
	if w := do("browse.example.test", "POST", "/api/v2/auth/login", "", map[string]string{"username": "preview-shared", "password": "synthetic-password"}); w.Code != http.StatusForbidden {
		t.Fatalf("password login on the preview host: %d", w.Code)
	}
	// ...and the shared account cannot sign in anywhere else either: the
	// early guard returns a named refusal before any session insert.
	if w := do("lib.example.test", "POST", "/api/v2/auth/login", "", map[string]string{"username": "preview-shared", "password": "synthetic-password"}); w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"preview_account"`)) {
		t.Fatalf("shared account login needs an explicit refusal: %d %s", w.Code, w.Body)
	}
	var nonGuest int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth_sessions WHERE user_id=$1 AND auth_method <> 'preview_guest'`, shared.ID).Scan(&nonGuest); err != nil || nonGuest != 0 {
		t.Fatalf("shared account holds %d non-guest sessions (%v)", nonGuest, err)
	}

	// Refresh never slides.
	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE auth_method='preview_guest' ORDER BY created_at DESC LIMIT 1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if w := do("browse.example.test", "POST", "/api/v2/auth/refresh", "", map[string]string{"refresh_token": session.RefreshToken}); w.Code != http.StatusOK {
		t.Fatalf("guest refresh: %d %s", w.Code, w.Body)
	}
	var after time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE auth_method='preview_guest' ORDER BY created_at DESC LIMIT 1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("refresh moved expires_at from %v to %v", before, after)
	}

	// No address or user agent is stored for the guest.
	var withIP int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth_sessions WHERE user_id=$1 AND (ip_address IS NOT NULL OR COALESCE(device_name,'') <> '')`, shared.ID).Scan(&withIP); err != nil {
		t.Fatal(err)
	}
	if withIP != 0 {
		t.Fatalf("%d guest sessions store an address or device name", withIP)
	}

	// Status.
	if w := do("browse.example.test", "GET", "/api/bloem/v1/preview/status", "", nil); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"storefront_url"`)) {
		t.Fatalf("status on the preview host: %d %s", w.Code, w.Body)
	}
	if w := do("lib.example.test", "GET", "/api/bloem/v1/preview/status", "", nil); bytes.Contains(w.Body.Bytes(), []byte(`"storefront_url"`)) {
		t.Fatalf("status elsewhere exposes landing details: %s", w.Body)
	}

	// Kill switch.
	if err := store.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	bloemPreviewReader(Dependencies{DB: pool}).Invalidate()
	if w := do("browse.example.test", "GET", "/api/v2/user/libraries", session.AccessToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("guest after kill switch: %d", w.Code)
	}
	if w := do("browse.example.test", "POST", "/api/bloem/v1/preview/sessions", "", map[string]string{"ticket": ticket()}); w.Code != http.StatusNotFound {
		t.Fatalf("exchange after kill switch: %d", w.Code)
	}
}
