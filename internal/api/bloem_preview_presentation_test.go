package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

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

func TestPreviewPersonalPresentationThroughRouter(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	create := func(name, role string) int {
		u, err := users.Create(ctx, models.CreateUserInput{Username: name, Email: name + "@example.test", Password: "synthetic-password", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	admin, personal, ordinary := create("present-admin", "admin"), create("present-personal", "user"), create("present-ordinary", "user")
	key, err := auth.NewAPIKeyRepository(pool).Create(ctx, admin, "preview", []string{auth.ScopeAdminPreview})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := auth.NewAPIKeyRepository(pool).Create(ctx, admin, "other", []string{auth.ScopeAdminUsers})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entitlements.NewTemplateStore(pool).ApplyDefaultAccountTemplate(ctx, admin, personal, accesspolicy.PreviewTemplateKey, 1, false); err != nil {
		t.Fatal(err)
	}
	const secret = "presentation-router-test"
	router := NewRouter(Dependencies{DB: pool, FolderRepo: catalog.NewFolderRepository(pool), FileRepo: scanner.NewFileRepository(pool), SessionMgr: playback.NewSessionManager(4, 2), UserStoreProvider: pgstore.NewPostgresProvider(pool), Config: &config.Config{Auth: config.AuthConfig{JWTSecret: secret, AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}}})
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
	prefix := "/api/bloem/v1/admin/platform/preview"
	if w := call("POST", prefix+"/account", key.Key, map[string]any{"issuer": "operator-test"}); w.Code != 201 {
		t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
	if w := call("PUT", prefix+"/settings", key.Key, map[string]any{"issuer": "operator-test", "brand": "Example", "cta_url": "https://store.example.test/trial", "cta_label": "Try Example", "item_cta_label": "Start your trial", "cta_message": "Seven days for one euro."}); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	deadline := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	path := prefix + "/accounts/" + strconv.Itoa(personal) + "/presentation"
	if w := call("PUT", path, unrelated.Key, map[string]any{"issuer": "operator-test", "ends_at": deadline}); w.Code != 403 {
		t.Fatalf("unrelated scope: %d", w.Code)
	}
	if w := call("PUT", path, key.Key, map[string]any{"issuer": "operator-test", "ends_at": deadline}); w.Code != 204 {
		t.Fatalf("provision: %d %s", w.Code, w.Body)
	}
	token := func(id int) string {
		sid := "presentation-" + strconv.Itoa(id)
		if err := auth.NewSessionRepository(pool).Create(ctx, models.AuthSession{ID: sid, UserID: id, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		token, err := auth.NewJWTService(secret, time.Hour, time.Hour).GenerateAccessToken(id, "user", sid)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	personalToken, ordinaryToken := token(personal), token(ordinary)
	var me struct {
		Preview         *preview.Presentation `json:"preview"`
		PlaybackAllowed *bool                 `json:"playback_allowed"`
	}
	w := call("GET", "/api/v2/account/me", personalToken, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || w.Code != 200 || me.Preview == nil || me.Preview.Kind != "personal" || me.Preview.EndsAt == nil || !me.Preview.EndsAt.Equal(deadline) || me.Preview.CTALabel != "Try Example" || me.Preview.ItemCTALabel != "Start your trial" || me.Preview.CTAMessage != "Seven days for one euro." || me.PlaybackAllowed == nil || *me.PlaybackAllowed {
		t.Fatalf("personal: %d %s", w.Code, w.Body)
	}
	w = call("GET", "/api/v2/account/me", ordinaryToken, nil)
	me.Preview = nil
	me.PlaybackAllowed = nil
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || w.Code != 200 || me.Preview != nil || me.PlaybackAllowed == nil || !*me.PlaybackAllowed {
		t.Fatalf("ordinary: %d %s", w.Code, w.Body)
	}
	if w := call("PUT", prefix+"/accounts/"+strconv.Itoa(ordinary)+"/presentation", key.Key, map[string]any{"issuer": "operator-test", "ends_at": deadline}); w.Code != 400 {
		t.Fatalf("non-preview provision: %d %s", w.Code, w.Body)
	}
	eventPath := "/api/bloem/v1/preview/events"
	if w := call("POST", eventPath, ordinaryToken, map[string]any{"event": "cta_shown"}); w.Code != 403 {
		t.Fatalf("non-preview event: %d %s", w.Code, w.Body)
	}
	if w := call("POST", eventPath, "", map[string]any{"event": "cta_shown"}); w.Code != 401 {
		t.Fatalf("anonymous event: %d", w.Code)
	}
	for _, body := range []any{map[string]any{"event": "unknown"}, map[string]any{"event": "cta_shown", "payload": "private"}} {
		if w := call("POST", eventPath, personalToken, body); w.Code != 400 {
			t.Fatalf("invalid event: %d %s", w.Code, w.Body)
		}
	}
	for _, event := range []string{"cta_shown", "cta_clicked", "session_ended"} {
		if w := call("POST", eventPath, personalToken, map[string]any{"event": event}); w.Code != 204 {
			t.Fatalf("event %s: %d %s", event, w.Code, w.Body)
		}
	}
	if w := call("POST", eventPath, personalToken, map[string]any{"event": "cta_shown"}); w.Code != 429 {
		t.Fatalf("event limit: %d", w.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT sum(count)::int FROM bloem_preview_event_counts`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("event counters: %d %v", count, err)
	}
	// A stale presentation record cannot make an ordinary account a preview.
	if _, err := entitlements.NewTemplateStore(pool).ApplyDefaultAccountTemplate(ctx, admin, personal, "standard", 1, false); err != nil {
		t.Fatal(err)
	}
	w = call("GET", "/api/v2/account/me", personalToken, nil)
	me.Preview = nil
	me.PlaybackAllowed = nil
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil || w.Code != 200 || me.Preview != nil {
		t.Fatalf("conversion kept preview: %d %s", w.Code, w.Body)
	}
}
