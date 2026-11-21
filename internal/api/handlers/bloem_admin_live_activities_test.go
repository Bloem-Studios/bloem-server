package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

type liveStartCall struct {
	userID   int
	deviceID string
	token    *string
	kinds    []string
}

// liveStub is an admin push backend that also keeps Live Activities.
type liveStub struct {
	adminPushStub
	liveOn     bool
	liveErr    error
	registered []notifications.LiveActivityRegistration
	userIDs    []int
	deviceIDs  []string
	ended      []string
	starts     []liveStartCall
}

func (s *liveStub) LiveActivitiesEnabled(context.Context) bool { return s.liveOn }
func (s *liveStub) RegisterLiveActivity(_ context.Context, userID int, deviceID string, in notifications.LiveActivityRegistration) error {
	s.userIDs, s.deviceIDs, s.registered = append(s.userIDs, userID), append(s.deviceIDs, deviceID), append(s.registered, in)
	return s.liveErr
}
func (s *liveStub) EndLiveActivity(_ context.Context, userID int, deviceID, activityID string) error {
	s.userIDs, s.deviceIDs, s.ended = append(s.userIDs, userID), append(s.deviceIDs, deviceID), append(s.ended, activityID)
	return s.liveErr
}
func (s *liveStub) SetLiveStart(_ context.Context, userID int, deviceID string, token *string, kinds []string) error {
	s.starts = append(s.starts, liveStartCall{userID, deviceID, token, kinds})
	return s.liveErr
}

func liveRouter(h *BloemAdminPushHandler, claims *auth.Claims) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if claims != nil {
				req = req.WithContext(middleware.SetClaims(req.Context(), claims))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/devices/{device_id}/live-activities", h.HandleRegisterLiveActivity)
	r.Delete("/devices/{device_id}/live-activities/{activity_id}", h.HandleEndLiveActivity)
	r.Put("/devices/{device_id}/live-activity-start", h.HandleSetLiveStart)
	return r
}

func liveAdmin() *auth.Claims {
	return &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess}
}

const liveRegisterBody = `{"activity_id":"6f1c2b9e-1d4a-4c7e-9a2b-000000000001","kind":"scan","subject_id":"7","platform":"ios","push_token":"ab12","environment":"sandbox"}`

func TestLiveActivityRoutesPassTheCallerAndWhatTheAppSaid(t *testing.T) {
	stub := &liveStub{liveOn: true}
	h := NewBloemAdminPushHandlerWith(stub)
	router := liveRouter(h, liveAdmin())

	rec := serveAdminPush(router, "POST", "/devices/dev1/live-activities", liveRegisterBody, "")
	if rec.Code != http.StatusCreated || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("register = %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	want := notifications.LiveActivityRegistration{ActivityID: "6f1c2b9e-1d4a-4c7e-9a2b-000000000001", Kind: "scan", SubjectID: "7", Platform: "ios", PushToken: "ab12", Environment: "sandbox"}
	if len(stub.registered) != 1 || stub.registered[0] != want || stub.userIDs[0] != 7 || stub.deviceIDs[0] != "dev1" {
		t.Fatalf("register reached the service as %+v for %v on %v", stub.registered, stub.userIDs, stub.deviceIDs)
	}
	android := `{"activity_id":"6f1c2b9e-1d4a-4c7e-9a2b-000000000002","kind":"backup","platform":"android"}`
	if rec := serveAdminPush(router, "POST", "/devices/dev1/live-activities", android, ""); rec.Code != http.StatusCreated {
		t.Fatalf("android register = %d", rec.Code)
	}

	if rec := serveAdminPush(router, "DELETE", "/devices/dev1/live-activities/6f1c2b9e-1d4a-4c7e-9a2b-000000000001", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("end = %d", rec.Code)
	}
	if len(stub.ended) != 1 || stub.ended[0] != "6f1c2b9e-1d4a-4c7e-9a2b-000000000001" {
		t.Fatalf("end reached the service as %v", stub.ended)
	}

	if rec := serveAdminPush(router, "PUT", "/devices/dev1/live-activity-start", `{"token":"cd34","kinds":["scan","backup"]}`, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("set start = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveAdminPush(router, "PUT", "/devices/dev1/live-activity-start", `{"token":null,"kinds":[]}`, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("clear start = %d", rec.Code)
	}
	if len(stub.starts) != 2 || stub.starts[0].token == nil || *stub.starts[0].token != "cd34" || strings.Join(stub.starts[0].kinds, ",") != "scan,backup" ||
		stub.starts[1].token != nil || len(stub.starts[1].kinds) != 0 || stub.starts[0].deviceID != "dev1" || stub.starts[0].userID != 7 {
		t.Fatalf("start reached the service as %+v", stub.starts)
	}

	// Unknown fields are refused, as on every admin push route.
	if rec := serveAdminPush(router, "POST", "/devices/dev1/live-activities", `{"activity_id":"x","kind":"scan","platform":"ios","title":"Movies"}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", rec.Code)
	}
}

func TestLiveActivityRoutesAcceptOnlyARealAdministratorSession(t *testing.T) {
	stub := &liveStub{liveOn: true}
	h := NewBloemAdminPushHandlerWith(stub)
	viewer := liveAdmin()
	viewer.Role = "user"
	apiKey := liveAdmin()
	apiKey.TokenType, apiKey.SessionID = auth.TokenTypeAPIKey, ""
	for name, tc := range map[string]struct {
		claims *auth.Claims
		status int
	}{"no claims": {nil, 401}, "viewer": {viewer, 403}, "api key": {apiKey, 403}} {
		router := liveRouter(h, tc.claims)
		for _, req := range [][3]string{
			{"POST", "/devices/dev1/live-activities", liveRegisterBody},
			{"DELETE", "/devices/dev1/live-activities/6f1c2b9e-1d4a-4c7e-9a2b-000000000001", ""},
			{"PUT", "/devices/dev1/live-activity-start", `{"token":null,"kinds":[]}`},
		} {
			if rec := serveAdminPush(router, req[0], req[1], req[2], ""); rec.Code != tc.status {
				t.Fatalf("%s %s %s = %d, want %d", name, req[0], req[1], rec.Code, tc.status)
			}
		}
	}
	if len(stub.registered)+len(stub.ended)+len(stub.starts) != 0 {
		t.Fatal("a refused caller reached the service")
	}
}

func TestLiveActivityRoutesMapServiceErrors(t *testing.T) {
	for err, status := range map[error]int{
		notifications.ErrAdminPushNotFound:    404,
		notifications.ErrAdminPushInvalid:     422,
		notifications.ErrAdminPushUnavailable: 503,
	} {
		stub := &liveStub{liveOn: true, liveErr: err}
		router := liveRouter(NewBloemAdminPushHandlerWith(stub), liveAdmin())
		if rec := serveAdminPush(router, "POST", "/devices/dev1/live-activities", liveRegisterBody, ""); rec.Code != status {
			t.Fatalf("%v: register = %d, want %d", err, rec.Code, status)
		}
		if rec := serveAdminPush(router, "PUT", "/devices/dev1/live-activity-start", `{"token":null,"kinds":[]}`, ""); rec.Code != status {
			t.Fatalf("%v: start = %d, want %d", err, rec.Code, status)
		}
	}
}

func TestCapabilityOnlyWithTheSwitch(t *testing.T) {
	probe := func(h *BloemSystemHandler) string {
		rec := httptest.NewRecorder()
		h.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
		return rec.Body.String()
	}
	if notifications.AdminLiveActivitiesCapabilityToken != "admin_live_activities_v1" {
		t.Fatalf("token = %q", notifications.AdminLiveActivitiesCapabilityToken)
	}
	if got := probe(NewBloemSystemHandler(nil)); strings.Contains(got, "admin_live_activities_v1") {
		t.Fatalf("a server without push advertised live activities: %s", got)
	}
	stub := &liveStub{}
	h := NewBloemAdminPushHandlerWith(stub)
	if !h.LiveAvailable() {
		t.Fatal("a backend that keeps Live Activities must make the routes available")
	}
	system := NewBloemSystemHandler(nil)
	system.SetAdminPushReadiness(h.Ready)
	system.SetAdminLiveActivitiesReadiness(h.LiveReady)
	for _, tc := range []struct {
		push, live, want bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		stub.enabled, stub.liveOn = tc.push, tc.live
		if got := strings.Contains(probe(system), `"admin_live_activities_v1"`); got != tc.want {
			t.Fatalf("push=%t switch=%t: advertised=%t, want %t", tc.push, tc.live, got, tc.want)
		}
	}
	// A backend without Live Activities (an older service) never advertises them.
	plain := NewBloemAdminPushHandlerWith(&adminPushStub{enabled: true})
	if plain.LiveAvailable() || plain.LiveReady(context.Background()) {
		t.Fatal("a backend without Live Activities must not offer them")
	}
}
