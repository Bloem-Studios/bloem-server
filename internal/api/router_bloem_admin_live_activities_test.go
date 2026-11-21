package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// liveAdminPush is a ready push backend that also keeps Live Activities.
type liveAdminPush struct {
	readyAdminPush
	switchOn bool
}

func (l liveAdminPush) LiveActivitiesEnabled(context.Context) bool { return l.switchOn }
func (liveAdminPush) RegisterLiveActivity(context.Context, int, string, notifications.LiveActivityRegistration) error {
	return nil
}
func (liveAdminPush) EndLiveActivity(context.Context, int, string, string) error { return nil }
func (liveAdminPush) SetLiveStart(context.Context, int, string, *string, []string) error {
	return nil
}

func serveLive(router http.Handler, method, path, body, bearer string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

func TestAdminLiveActivityRoutesAreMountedBehindTheLoginAndAdvertisedWithTheSwitch(t *testing.T) {
	claims := &auth.Claims{UserID: 7, Role: "admin", SessionID: "s", TokenType: auth.TokenTypeAccess}
	backend := liveAdminPush{readyAdminPush: readyAdminPush{enabled: true}}
	router, _ := adminPushMountedRouter(handlers.NewBloemAdminPushHandlerWith(backend), claims)
	register := `{"activity_id":"6f1c2b9e-1d4a-4c7e-9a2b-000000000001","kind":"backup","platform":"android"}`
	routes := [][3]string{
		{"POST", "/admin/push/devices/dev1/live-activities", register},
		{"DELETE", "/admin/push/devices/dev1/live-activities/6f1c2b9e-1d4a-4c7e-9a2b-000000000001", ""},
		{"PUT", "/admin/push/devices/dev1/live-activity-start", `{"token":null,"kinds":[]}`},
	}
	for _, route := range routes {
		if code := serveLive(router, route[0], route[1], route[2], ""); code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a login = %d, want 401", route[0], route[1], code)
		}
	}
	for i, want := range []int{http.StatusCreated, http.StatusNoContent, http.StatusNoContent} {
		if code := serveLive(router, routes[i][0], routes[i][1], routes[i][2], "jwt"); code != want {
			t.Fatalf("%s %s = %d, want %d", routes[i][0], routes[i][1], code, want)
		}
	}
	if rec := get(router, "GET", "/capabilities", ""); strings.Contains(rec.Body.String(), "admin_live_activities_v1") {
		t.Fatalf("live activities advertised with the operator's switch off: %s", rec.Body.String())
	}
	on, _ := adminPushMountedRouter(handlers.NewBloemAdminPushHandlerWith(liveAdminPush{readyAdminPush: readyAdminPush{enabled: true}, switchOn: true}), claims)
	if rec := get(on, "GET", "/capabilities", ""); !strings.Contains(rec.Body.String(), `"admin_live_activities_v1"`) {
		t.Fatalf("live activities not advertised with the switch on: %s", rec.Body.String())
	}

	// A backend without Live Activities mounts none of the routes.
	plain, _ := adminPushMountedRouter(handlers.NewBloemAdminPushHandlerWith(readyAdminPush{enabled: true}), claims)
	if code := serveLive(plain, routes[0][0], routes[0][1], routes[0][2], "jwt"); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Fatalf("a backend without Live Activities answered %d", code)
	}
}
