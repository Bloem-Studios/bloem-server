package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

type readyAdminPush struct{ enabled bool }

func (readyAdminPush) Available() bool { return true }
func (r readyAdminPush) Enabled(context.Context) bool {
	return r.enabled
}
func (readyAdminPush) Register(context.Context, int, notifications.AdminPushRegistration) (*notifications.AdminPushRegistered, error) {
	return &notifications.AdminPushRegistered{DeviceID: "dev1", DisplayToken: "bad_x"}, nil
}
func (readyAdminPush) SetQuietHours(_ context.Context, _ int, _ string, q notifications.AdminPushQuietHours) (notifications.AdminPushQuietHours, error) {
	return q, nil
}
func (readyAdminPush) ClearQuietHours(context.Context, int, string) error { return nil }
func (readyAdminPush) SetKinds(context.Context, int, string, map[string]bool) ([]notifications.AdminPushKindState, error) {
	return nil, nil
}
func (readyAdminPush) SetWidgets(_ context.Context, _ int, _ string, placed bool, channels []string) (bool, []string, error) {
	return placed, channels, nil
}
func (readyAdminPush) Unregister(context.Context, int, string) error { return nil }
func (readyAdminPush) Retire(context.Context, string, string) error  { return nil }
func (readyAdminPush) Content(_ context.Context, token, _ string) (*notifications.AdminPushContent, error) {
	if token != "bad_x" {
		return nil, notifications.ErrAdminPushUnauthorized
	}
	return &notifications.AdminPushContent{DeliveryID: "d1", Kind: notifications.AdminKindBackupFailed, Channel: "backups", Title: "Backup failed on Home"}, nil
}

// adminSessions is a live login session whose account is an administrator now.
type adminSessions struct{ bloemSessionValidator }

func (adminSessions) ActiveSession(context.Context, string, int) (auth.SessionAuthority, bool, error) {
	return auth.SessionAuthority{Role: "admin", Purpose: "account"}, true, nil
}

func adminPushMountedRouter(h *handlers.BloemAdminPushHandler, claims *auth.Claims) (http.Handler, *handlers.BloemSystemHandler) {
	authMW := apimw.NewAuthMiddleware(bloemTokenValidator{claims: claims}, adminSessions{}, nil, nil)
	system := handlers.NewBloemSystemHandler(nil)
	router := chi.NewRouter()
	router.Get("/capabilities", system.HandleCapabilities)
	mountBloemAdminPush(router, h, system, authMW)
	return router, system
}

func get(router http.Handler, method, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// A plain Silo server, or a Bloem build with no push backing, mounts nothing and
// advertises nothing: the apps then treat it as unable to push.
func TestAdminPushIsAbsentWithoutABackend(t *testing.T) {
	router, _ := adminPushMountedRouter(nil, &auth.Claims{UserID: 7, Role: "admin", SessionID: "s", TokenType: auth.TokenTypeAccess})
	for _, req := range [][2]string{{"POST", "/admin/push/devices"}, {"GET", "/admin/push/content/x"}, {"POST", "/admin/push/devices/d/retire"}} {
		if rec := get(router, req[0], req[1], "tok"); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d, want it unmounted", req[0], req[1], rec.Code)
		}
	}
	if rec := get(router, "GET", "/capabilities", ""); strings.Contains(rec.Body.String(), "admin_push") {
		t.Fatalf("capabilities advertise admin push with no backend: %s", rec.Body.String())
	}
	if NewRouter(Dependencies{}) == nil || bloemAdminPushHandler(Dependencies{}) != nil {
		t.Fatal("a Dependencies with no notification system must yield no admin push handler")
	}
}

func TestAdminPushMountsRoutesAndAdvertisesTheTokenOnlyWhileEnabled(t *testing.T) {
	backend := readyAdminPush{enabled: true}
	router, _ := adminPushMountedRouter(handlers.NewBloemAdminPushHandlerWith(backend), &auth.Claims{UserID: 7, Role: "admin", SessionID: "s", TokenType: auth.TokenTypeAccess})
	if rec := get(router, "GET", "/capabilities", ""); !strings.Contains(rec.Body.String(), `"admin_push_v1"`) {
		t.Fatalf("a ready server did not advertise admin_push_v1: %s", rec.Body.String())
	}
	body := `{"installation_id":"install-0001","platform":"android","token":"x"}`
	req := httptest.NewRequest("POST", "/admin/push/devices", strings.NewReader(body))
	// Registration is behind the login gate.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("registration without a bearer = %d, want 401", rec.Code)
	}
	req = httptest.NewRequest("POST", "/admin/push/devices", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer jwt")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"display_token":"bad_x"`) {
		t.Fatalf("registration = %d %s", rec.Code, rec.Body.String())
	}
	// Reading a notification and retiring a device are not behind the login
	// gate: their credential is the display token.
	if rec := get(router, "GET", "/admin/push/content/dev1.d1", "bad_x"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Backup failed on Home") {
		t.Fatalf("content with a display token = %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(router, "GET", "/admin/push/content/dev1.d1", "bad_other"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("content with a wrong token = %d, want 401", rec.Code)
	}
	if rec := get(router, "POST", "/admin/push/devices/dev1/retire", "bad_x"); rec.Code != http.StatusNoContent {
		t.Fatalf("retire = %d, want 204", rec.Code)
	}

	// The operator switching push delivery off withdraws the token at once.
	off, _ := adminPushMountedRouter(handlers.NewBloemAdminPushHandlerWith(readyAdminPush{enabled: false}), nil)
	if rec := get(off, "GET", "/capabilities", ""); strings.Contains(rec.Body.String(), "admin_push") {
		t.Fatalf("delivery off still advertises admin push: %s", rec.Body.String())
	}
}
