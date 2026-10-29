package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

func TestPreviewAdminRouterScopedControlPlane(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	admin, err := users.Create(ctx, models.CreateUserInput{Username: "preview-operator", Email: "operator@example.test", Password: "synthetic-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.NewAPIKeyRepository(pool).Create(ctx, admin.ID, "preview-control", []string{"admin:preview"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.NewAPIKeyRepository(pool).Create(ctx, admin.ID, "ordinary", []string{auth.ScopeAdminUsers})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{DB: pool, FolderRepo: catalog.NewFolderRepository(pool), FileRepo: scanner.NewFileRepository(pool), SessionMgr: playback.NewSessionManager(4, 2), UserStoreProvider: pgstore.NewPostgresProvider(pool), Config: &config.Config{Server: config.ServerConfig{PublicURL: "https://configured.example.test"}, Auth: config.AuthConfig{JWTSecret: "preview-control-test", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}}})
	call := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		var b bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&b).Encode(body)
		}
		r := httptest.NewRequest(method, path, &b)
		r.Host = "lib.example.test"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	const prefix = "/api/bloem/v1/admin/platform/preview"
	w := call("POST", prefix+"/account", key.Key, map[string]any{"issuer": "operator-test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("ensure: %d %s", w.Code, w.Body)
	}
	var first struct {
		AccountID int `json:"account_id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &first); err != nil || first.AccountID <= 0 {
		t.Fatalf("account response: %s", w.Body)
	}
	if w = call("POST", prefix+"/account", key.Key, map[string]any{"issuer": "another-operator"}); w.Code != http.StatusConflict {
		t.Fatalf("issuer takeover: %d %s", w.Code, w.Body)
	}
	if w = call("GET", prefix+"/settings", other.Key, nil); w.Code != http.StatusForbidden {
		t.Fatalf("unrelated scope admitted: %d %s", w.Code, w.Body)
	}
	if w = call("GET", "/api/v2/admin/users", key.Key, nil); w.Code != http.StatusForbidden {
		t.Fatalf("preview scope escaped: %d %s", w.Code, w.Body)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if w = call("PUT", prefix+"/keys", key.Key, map[string]any{"issuer": "operator-test", "public_keys": []string{base64.StdEncoding.EncodeToString(pub)}}); w.Code != http.StatusNoContent {
		t.Fatalf("keys: %d %s", w.Code, w.Body)
	}
	for _, host := range []string{"lib.example.test", "configured.example.test", "browse.example.test"} {
		w = call("PUT", prefix+"/settings", key.Key, map[string]any{"issuer": "operator-test", "host": host, "enabled": true})
		want := http.StatusNoContent
		if host != "browse.example.test" {
			want = http.StatusBadRequest
		}
		if w.Code != want {
			t.Fatalf("host %s: %d %s", host, w.Code, w.Body)
		}
	}
	if w = call("GET", prefix+"/capacity", key.Key, nil); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"account_ready":true`)) {
		t.Fatalf("capacity: %d %s", w.Code, w.Body)
	}
	if w = call("PUT", prefix+"/settings", key.Key, map[string]any{"issuer": "operator-test", "session_ttl_seconds": int64(36028797018971168)}); w.Code != http.StatusBadRequest {
		t.Fatalf("overflow seconds accepted: %d %s", w.Code, w.Body)
	}
	sessionID := uuid.NewString()
	if err = auth.NewSessionRepository(pool).Create(ctx, models.AuthSession{ID: sessionID, UserID: admin.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	contextToken, err := auth.NewAdminContextTokenService("preview-control-test").Mint(auth.AdminContextClaims{AccountID: admin.ID, AccountIncarnationID: admin.AccountIncarnationID, Scope: auth.AdminScopePlatform, SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if w = call("GET", prefix+"/settings", contextToken, nil); w.Code != http.StatusOK {
		t.Fatalf("Platform context refused: %d %s", w.Code, w.Body)
	}
	// The storefront signs free-play allowances only for a server that says
	// it meters them.
	if !bytes.Contains(w.Body.Bytes(), []byte(`"free_play_supported":true`)) {
		t.Fatalf("settings read does not report free play support: %s", w.Body)
	}
	nonAdmin, err := users.Create(ctx, models.CreateUserInput{Username: "preview-ordinary", Email: "ordinary@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	nonAdminKey, err := auth.NewAPIKeyRepository(pool).Create(ctx, nonAdmin.ID, "preview", []string{"admin:preview"})
	if err != nil {
		t.Fatal(err)
	}
	if w = call("GET", prefix+"/settings", nonAdminKey.Key, nil); w.Code != http.StatusForbidden {
		t.Fatalf("scope granted authority to user: %d %s", w.Code, w.Body)
	}
	if w = call("PUT", prefix+"/settings", key.Key, map[string]any{"issuer": "operator-test", "enabled": false, "account_id": admin.ID}); w.Code != http.StatusBadRequest {
		t.Fatalf("account reassigned: %d %s", w.Code, w.Body)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if w = call("GET", prefix+"/settings", key.Key, nil); w.Code < 400 {
		t.Fatalf("disabled operator admitted: %d", w.Code)
	}
}
