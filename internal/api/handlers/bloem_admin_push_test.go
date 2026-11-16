package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

type widgetsCall struct {
	placed   bool
	channels []string
}

type adminPushStub struct {
	registered  []notifications.AdminPushRegistration
	registerFor []int
	err         error
	token       string
	deliveryID  string
	retired     string
	kinds       map[string]bool
	widgets     widgetsCall
	content     *notifications.AdminPushContent
	enabled     bool
}

func (s *adminPushStub) Available() bool              { return true }
func (s *adminPushStub) Enabled(context.Context) bool { return s.enabled }
func (s *adminPushStub) Register(_ context.Context, userID int, reg notifications.AdminPushRegistration) (*notifications.AdminPushRegistered, error) {
	s.registerFor = append(s.registerFor, userID)
	s.registered = append(s.registered, reg)
	if s.err != nil {
		return nil, s.err
	}
	return &notifications.AdminPushRegistered{DeviceID: "dev1", DisplayToken: "bad_x", Kinds: []notifications.AdminPushKindState{{Kind: "admin.scan_failed", Enabled: true, ServerEmitted: true}}, WidgetChannels: []string{}}, nil
}
func (s *adminPushStub) SetKinds(_ context.Context, _ int, _ string, kinds map[string]bool) ([]notifications.AdminPushKindState, error) {
	s.kinds = kinds
	return nil, s.err
}
func (s *adminPushStub) SetWidgets(_ context.Context, _ int, _ string, placed bool, channels []string) (bool, []string, error) {
	s.widgets = widgetsCall{placed, channels}
	return placed, channels, s.err
}
func (s *adminPushStub) Unregister(context.Context, int, string) error { return s.err }
func (s *adminPushStub) Retire(_ context.Context, token, deviceID string) error {
	s.token, s.retired = token, deviceID
	return s.err
}
func (s *adminPushStub) Content(_ context.Context, token, deliveryID string) (*notifications.AdminPushContent, error) {
	s.token, s.deliveryID = token, deliveryID
	if s.err != nil {
		return nil, s.err
	}
	return s.content, nil
}

func adminPushRouter(h *BloemAdminPushHandler, claims *auth.Claims) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if claims != nil {
					req = req.WithContext(middleware.SetClaims(req.Context(), claims))
				}
				next.ServeHTTP(w, req)
			})
		})
		r.Post("/devices", h.HandleRegister)
		r.Patch("/devices/{device_id}/kinds", h.HandleSetKinds)
		r.Put("/devices/{device_id}/widgets", h.HandleSetWidgets)
		r.Delete("/devices/{device_id}", h.HandleDelete)
	})
	r.Post("/devices/{device_id}/retire", h.HandleRetire)
	r.Get("/content/{delivery_id}", h.HandleContent)
	return r
}

func serveAdminPush(h http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const registerBody = `{"installation_id":"install-0001","platform":"apple","token":"abc","apns_environment":"sandbox","apns_topic":"com.bloemstudios.admin"}`

func TestAdminPushRoutesAcceptOnlyARealAdministratorSession(t *testing.T) {
	impersonator := 3
	admin := func(mutate func(*auth.Claims)) *auth.Claims {
		c := &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess}
		if mutate != nil {
			mutate(c)
		}
		return c
	}
	for name, tc := range map[string]struct {
		claims *auth.Claims
		status int
		code   string
	}{
		"no claims":       {nil, 401, "unauthorized"},
		"viewer":          {admin(func(c *auth.Claims) { c.Role = "user" }), 403, "admin_required"},
		"api key":         {admin(func(c *auth.Claims) { c.TokenType = auth.TokenTypeAPIKey; c.SessionID = "" }), 403, "account_session_required"},
		"no login":        {admin(func(c *auth.Claims) { c.SessionID = "" }), 403, "account_session_required"},
		"impersonated":    {admin(func(c *auth.Claims) { c.ImpersonatorUserID = &impersonator }), 403, "account_session_required"},
		"administrator":   {admin(nil), 200, ""},
		"refresh token":   {admin(func(c *auth.Claims) { c.TokenType = auth.TokenTypeRefresh }), 403, "account_session_required"},
		"zero user":       {admin(func(c *auth.Claims) { c.UserID = 0 }), 401, "unauthorized"},
		"disabled role":   {admin(func(c *auth.Claims) { c.Role = "" }), 403, "admin_required"},
		"support context": {admin(func(c *auth.Claims) { c.Role = "support" }), 403, "admin_required"},
	} {
		t.Run(name, func(t *testing.T) {
			stub := &adminPushStub{}
			rec := serveAdminPush(adminPushRouter(NewBloemAdminPushHandlerWith(stub), tc.claims), "POST", "/devices", registerBody, "")
			if rec.Code != tc.status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tc.status)
			}
			if tc.code != "" && !strings.Contains(rec.Body.String(), `"error":"`+tc.code+`"`) {
				t.Fatalf("body = %s, want error %s", rec.Body.String(), tc.code)
			}
			if (len(stub.registered) == 1) != (tc.status == 200) {
				t.Fatalf("service called %d times for status %d", len(stub.registered), tc.status)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("registration answers must not be cached")
			}
		})
	}
}

func TestAdminPushRegisterPassesTheLoginSessionAndReturnsTheDisplayToken(t *testing.T) {
	stub := &adminPushStub{}
	h := adminPushRouter(NewBloemAdminPushHandlerWith(stub), &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess})
	rec := serveAdminPush(h, "POST", "/devices", registerBody, "")
	var out BloemAdminPushRegistered
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.DeviceID != "dev1" || out.DisplayToken != "bad_x" || len(out.Kinds) != 1 {
		t.Fatalf("response %s (%v)", rec.Body.String(), err)
	}
	if got := stub.registered[0]; got.LoginSessionID != "sess-1" || got.Platform != "apple" || got.APNsTopic != "com.bloemstudios.admin" || stub.registerFor[0] != 7 {
		t.Fatalf("service got %+v for user %v", got, stub.registerFor)
	}
	for name, body := range map[string]string{
		"unknown field": `{"installation_id":"install-0001","platform":"apple","token":"abc","role":"admin"}`,
		"not json":      `nope`,
	} {
		before := len(stub.registered)
		if rec := serveAdminPush(h, "POST", "/devices", body, ""); rec.Code != 400 || len(stub.registered) != before {
			t.Fatalf("%s: status %d, service calls %d->%d", name, rec.Code, before, len(stub.registered))
		}
	}
}

func TestAdminPushMapsServiceErrorsToStatuses(t *testing.T) {
	claims := &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess}
	for err, want := range map[error]int{
		notifications.ErrAdminPushUnavailable:  503,
		notifications.ErrAdminPushInvalid:      422,
		notifications.ErrAdminPushNotFound:     404,
		notifications.ErrAdminPushUnauthorized: 401,
		notifications.ErrAdminPushForbidden:    403,
		errors.New("database down"):            503,
	} {
		h := adminPushRouter(NewBloemAdminPushHandlerWith(&adminPushStub{err: err}), claims)
		if rec := serveAdminPush(h, "POST", "/devices", registerBody, ""); rec.Code != want {
			t.Errorf("%v: status %d, want %d", err, rec.Code, want)
		}
	}
	// A raw database error must not reach the client.
	h := adminPushRouter(NewBloemAdminPushHandlerWith(&adminPushStub{err: errors.New("pq: password authentication failed for user x")}), claims)
	if body := serveAdminPush(h, "POST", "/devices", registerBody, "").Body.String(); strings.Contains(body, "password") {
		t.Fatalf("internal error leaked: %s", body)
	}
}

func TestAdminPushKindsAndDeleteAreForTheSignedInAdministratorOnly(t *testing.T) {
	stub := &adminPushStub{}
	claims := &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess}
	h := adminPushRouter(NewBloemAdminPushHandlerWith(stub), claims)
	if rec := serveAdminPush(h, "PATCH", "/devices/dev1/kinds", `{"kinds":{"admin.scan_failed":false}}`, ""); rec.Code != 200 || stub.kinds["admin.scan_failed"] != false || len(stub.kinds) != 1 {
		t.Fatalf("kinds: %d %s %v", rec.Code, rec.Body.String(), stub.kinds)
	}
	if rec := serveAdminPush(h, "DELETE", "/devices/dev1", "", ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	viewer := adminPushRouter(NewBloemAdminPushHandlerWith(stub), &auth.Claims{UserID: 8, Role: "user", SessionID: "s", TokenType: auth.TokenTypeAccess})
	for _, req := range [][3]string{{"PATCH", "/devices/dev1/kinds", `{"kinds":{}}`}, {"DELETE", "/devices/dev1", ""}} {
		if rec := serveAdminPush(viewer, req[0], req[1], req[2], ""); rec.Code != 403 {
			t.Fatalf("%s as a viewer: %d", req[0], rec.Code)
		}
	}
}

func TestAdminPushContentAndRetireTakeTheDisplayTokenNotALogin(t *testing.T) {
	badge := 3
	stub := &adminPushStub{content: &notifications.AdminPushContent{
		DeliveryID: "d1", Kind: "admin.request_waiting", Channel: "requests", Title: "sam requested Dune", Body: "3 waiting", LockScreen: "New request waiting",
		Page: "requests", ItemID: "r1", SubjectType: "request", Actions: nil, Badge: &badge, CreatedAt: time.Unix(1, 0).UTC(),
	}}
	h := adminPushRouter(NewBloemAdminPushHandlerWith(stub), nil) // no login at all

	rec := serveAdminPush(h, "GET", "/content/dev1.d1", "", "bad_secret")
	if rec.Code != 200 || stub.token != "bad_secret" || stub.deliveryID != "dev1.d1" {
		t.Fatalf("content: %d %s token=%q id=%q", rec.Code, rec.Body.String(), stub.token, stub.deliveryID)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("notification text must not be cached")
	}
	var wire map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &wire)
	if actions, ok := wire["actions"].([]any); !ok || len(actions) != 0 {
		t.Fatalf("actions must be an empty array, got %v", wire["actions"])
	}
	if wire["badge"] != float64(3) || wire["page"] != "requests" || wire["item_id"] != "r1" || wire["lock_screen"] != "New request waiting" {
		t.Fatalf("content = %v", wire)
	}

	// No bearer reaches the service as an empty token, which it refuses.
	stub.err = notifications.ErrAdminPushUnauthorized
	if rec := serveAdminPush(h, "GET", "/content/d1", "", ""); rec.Code != 401 || stub.token != "" {
		t.Fatalf("no token: %d token=%q", rec.Code, stub.token)
	}
	stub.err = nil
	if rec := serveAdminPush(h, "POST", "/devices/dev1/retire", "", "bad_secret"); rec.Code != 204 || stub.retired != "dev1" || stub.token != "bad_secret" {
		t.Fatalf("retire: %d device=%q token=%q", rec.Code, stub.retired, stub.token)
	}
}

func TestAdminPushCapabilityIsAdvertisedOnlyWhileReady(t *testing.T) {
	probe := func(h *BloemSystemHandler) string {
		rec := httptest.NewRecorder()
		h.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
		return rec.Body.String()
	}
	plain := NewBloemSystemHandler(nil)
	if strings.Contains(probe(plain), notifications.AdminPushCapabilityToken) {
		t.Fatal("a server with no admin push must not advertise it")
	}
	stub := &adminPushStub{}
	system := NewBloemSystemHandler(nil)
	system.SetAdminPushReadiness(NewBloemAdminPushHandlerWith(stub).Ready)
	if got := probe(system); strings.Contains(got, "admin_push_v1") || strings.Contains(got, "admin_widget_wakes_v1") {
		t.Fatalf("push delivery switched off must withdraw both tokens: %s", got)
	}
	stub.enabled = true
	if got := probe(system); !strings.Contains(got, `"admin_push_v1"`) || !strings.Contains(got, `"admin_widget_wakes_v1"`) {
		t.Fatalf("a ready server must advertise admin_push_v1 and admin_widget_wakes_v1: %s", got)
	}
	stub.enabled = false
	if strings.Contains(probe(system), "admin_push_v1") {
		t.Fatal("the token must follow the operator's switch on every probe")
	}
}

func TestAdminPushWidgetReportIsForTheSignedInAdministratorAndPassesWhatTheAppSaid(t *testing.T) {
	stub := &adminPushStub{}
	claims := &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess}
	h := adminPushRouter(NewBloemAdminPushHandlerWith(stub), claims)
	rec := serveAdminPush(h, "PUT", "/devices/dev1/widgets", `{"widgets_placed":true,"channels":["requests","nodes"]}`, "")
	if rec.Code != 200 || !stub.widgets.placed || strings.Join(stub.widgets.channels, ",") != "requests,nodes" {
		t.Fatalf("report: %d %s %+v", rec.Code, rec.Body.String(), stub.widgets)
	}
	var out BloemAdminPushWidgets
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.WidgetsPlaced || len(out.Channels) != 2 {
		t.Fatalf("response %s (%v)", rec.Body.String(), err)
	}
	if rec := serveAdminPush(h, "PUT", "/devices/dev1/widgets", `{"widgets_placed":false,"channels":[]}`, ""); rec.Code != 200 || stub.widgets.placed {
		t.Fatalf("removing the last widget: %d %+v", rec.Code, stub.widgets)
	}
	if rec := serveAdminPush(h, "PUT", "/devices/dev1/widgets", `{"widgets_placed":true,"channels":["nodes"],"extra":1}`, ""); rec.Code != 400 {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	viewer := adminPushRouter(NewBloemAdminPushHandlerWith(stub), &auth.Claims{UserID: 8, Role: "user", SessionID: "s", TokenType: auth.TokenTypeAccess})
	if rec := serveAdminPush(viewer, "PUT", "/devices/dev1/widgets", `{"widgets_placed":true,"channels":["nodes"]}`, ""); rec.Code != 403 {
		t.Fatalf("a viewer reported widgets: %d", rec.Code)
	}
	for err, want := range map[error]int{notifications.ErrAdminPushInvalid: 422, notifications.ErrAdminPushNotFound: 404, notifications.ErrAdminPushUnavailable: 503} {
		h := adminPushRouter(NewBloemAdminPushHandlerWith(&adminPushStub{err: err}), claims)
		if rec := serveAdminPush(h, "PUT", "/devices/dev1/widgets", `{"widgets_placed":true,"channels":["nodes"]}`, ""); rec.Code != want {
			t.Errorf("%v: status %d, want %d", err, rec.Code, want)
		}
	}
}

func TestAdminPushRegistrationPassesTheWidgetReportAndEchoesIt(t *testing.T) {
	stub := &adminPushStub{}
	h := adminPushRouter(NewBloemAdminPushHandlerWith(stub), &auth.Claims{UserID: 7, Role: "admin", SessionID: "sess-1", TokenType: auth.TokenTypeAccess})
	body := `{"installation_id":"install-0001","platform":"android","token":"abc","widgets_placed":true,"widget_channels":["sessions"]}`
	rec := serveAdminPush(h, "POST", "/devices", body, "")
	if rec.Code != 200 {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	got := stub.registered[0]
	if got.WidgetsPlaced == nil || !*got.WidgetsPlaced || got.WidgetChannels == nil || (*got.WidgetChannels)[0] != "sessions" {
		t.Fatalf("service got %+v", got)
	}
	var wire map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &wire)
	if _, ok := wire["widgets_placed"]; !ok {
		t.Fatalf("response lacks widgets_placed: %s", rec.Body.String())
	}
	if channels, ok := wire["widget_channels"].([]any); !ok || channels == nil {
		t.Fatalf("widget_channels must be an array, got %v", wire["widget_channels"])
	}
	// Omitted fields reach the service as nil, which keeps what the server holds.
	stub.registered = nil
	serveAdminPush(h, "POST", "/devices", registerBody, "")
	if g := stub.registered[0]; g.WidgetsPlaced != nil || g.WidgetChannels != nil {
		t.Fatalf("an omitted widget report must stay nil: %+v", g)
	}
}
