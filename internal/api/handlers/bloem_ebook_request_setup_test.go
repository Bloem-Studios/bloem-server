package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type ebookSetupStub struct {
	calls         int
	platformCalls int
	platformError error
	v             bookrequests.Viewer
	enabled       bool
	in            EbookRequestConnectionInput
	tested        string
}

func (s *ebookSetupStub) List(context.Context, bookrequests.Viewer) (EbookRequestSetup, error) {
	s.calls++
	return EbookRequestSetup{Available: true, Enabled: s.enabled, Integrations: []bookrequests.EbookIntegration{}, Libraries: []bookrequests.EbookLibrary{}, Installations: []EbookRequestInstallation{}}, nil
}
func (s *ebookSetupStub) Create(_ context.Context, v bookrequests.Viewer, in EbookRequestConnectionInput) (bookrequests.EbookIntegration, error) {
	s.calls++
	s.v = v
	s.in = in
	return bookrequests.EbookIntegration{ID: "100123", Name: in.Name}, nil
}
func (s *ebookSetupStub) SetLibrary(_ context.Context, v bookrequests.Viewer, id string, library int) (bookrequests.EbookIntegration, error) {
	s.calls++
	s.v = v
	return bookrequests.EbookIntegration{ID: id, LibraryID: library}, nil
}
func (s *ebookSetupStub) SetEnabled(_ context.Context, v bookrequests.Viewer, enabled bool) error {
	s.calls++
	s.v = v
	s.enabled = enabled
	return nil
}
func (s *ebookSetupStub) Test(_ context.Context, v bookrequests.Viewer, id string) (RequestIntegrationTestResult, error) {
	s.calls++
	s.v = v
	s.tested = id
	return RequestIntegrationTestResult{OK: true, Message: "Bookwarehouse monitoring API authenticated."}, nil
}
func ebookSetupRequest(method, body, authority string, defaultOrg bool) *http.Request {
	r := organizationRequest(method, "/organization/ebook-request-policy", body, uuid.New(), 1, nil)
	tenant, _ := tenancy.FromContext(r.Context())
	tenant.OrganizationDefault = defaultOrg
	claims, _ := apimw.GetAdminContextClaims(r.Context())
	claims.EffectiveAuthority = authority
	return r.WithContext(apimw.SetAdminContextClaims(tenancy.WithContext(r.Context(), tenant), claims))
}
func ebookSetupHandler(s *ebookSetupStub) *BloemBookRequestsHandler {
	h := NewBloemBookRequestsHandler(&bookRequestsServiceStub{}, nil)
	h.SetEbookRequests(&ebookRequestsServiceStub{})
	h.SetEbookRequestSetup(s)
	return h.EbookRequests()
}
func TestBloemEbookRequestSetupRequiresPlatformAuthorityInDefaultOrganization(t *testing.T) {
	for _, tc := range []struct {
		authority  string
		defaultOrg bool
	}{{"organization_admin", true}, {"platform_admin", false}} {
		s := &ebookSetupStub{}
		h := ebookSetupHandler(s)
		for _, handle := range []http.HandlerFunc{h.HandleEbookRequestPolicy, h.HandleEbookIntegrationCreate, h.HandleEbookIntegrationLibrary, h.HandleEbookIntegrationTest} {
			w := httptest.NewRecorder()
			handle(w, ebookSetupRequest(http.MethodPut, `{"enabled":true}`, tc.authority, tc.defaultOrg))
			if w.Code != http.StatusForbidden || s.calls != 0 {
				t.Fatalf("authority reached configuration: %d %s calls=%d", w.Code, w.Body.String(), s.calls)
			}
		}
	}
}
func TestBloemEbookRequestPolicyRequiresExplicitBooleanAndBoundOrganization(t *testing.T) {
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"true"}`, `{"enabled":true,"organization_id":"other"}`, `{"enabled":true}{}`} {
		s := &ebookSetupStub{}
		w := httptest.NewRecorder()
		ebookSetupHandler(s).HandleEbookRequestPolicy(w, ebookSetupRequest(http.MethodPut, body, "platform_admin", true))
		if w.Code != http.StatusBadRequest || s.calls != 0 {
			t.Fatalf("invalid policy: %d %s calls=%d", w.Code, w.Body.String(), s.calls)
		}
	}
	s := &ebookSetupStub{}
	r := ebookSetupRequest(http.MethodPut, `{"enabled":true}`, "platform_admin", true)
	w := httptest.NewRecorder()
	ebookSetupHandler(s).HandleEbookRequestPolicy(w, r)
	tenant, _ := tenancy.FromContext(r.Context())
	if w.Code != http.StatusOK || s.calls != 1 || !s.enabled || s.v.OrganizationID != tenant.OrganizationID || s.v.UserID != 7 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("policy response/authority: %d %s %+v", w.Code, w.Body.String(), s)
	}
}
func TestBloemEbookRequestSetupDoesNotExposeHostedConfiguration(t *testing.T) {
	s := &ebookSetupStub{}
	w := httptest.NewRecorder()
	ebookSetupHandler(s).HandleEbookIntegrationList(w, ebookSetupRequest(http.MethodGet, "", "organization_admin", false))
	if w.Code != http.StatusOK || s.calls != 0 || !strings.Contains(w.Body.String(), `"available":false`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("hosted setup: %d %s", w.Code, w.Body.String())
	}
}

func TestBloemEbookRequestSetupConnectionUsesBoundAuthority(t *testing.T) {
	s := &ebookSetupStub{}
	w := httptest.NewRecorder()
	r := ebookSetupRequest(http.MethodPost, `{"name":"Books","base_url":"http://local.example.test","api_key":"synthetic-key","installation_id":12,"capability_id":"bookwarehouse","allow_http":true}`, "platform_admin", true)
	ebookSetupHandler(s).HandleEbookIntegrationCreate(w, r)
	if w.Code != http.StatusCreated || s.calls != 1 || s.v.UserID != 7 || !s.v.Admin || !s.in.AllowHTTP || s.in.InstallationID != 12 || s.in.APIKey != "synthetic-key" || strings.Contains(w.Body.String(), "synthetic-key") {
		t.Fatalf("connection input/response: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"supported_media_types":["audiobook"]}`, `{"plugin_config":{"allow_pdf":true}}`, `{"owner_id":"foreign"}`} {
		w = httptest.NewRecorder()
		before := s.calls
		ebookSetupHandler(s).HandleEbookIntegrationCreate(w, ebookSetupRequest(http.MethodPost, body, "platform_admin", true))
		if w.Code != http.StatusBadRequest || s.calls != before {
			t.Fatalf("unsupported connection authority reached setup: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestBloemEbookRequestConnectionTestUsesPathAndBoundAuthority(t *testing.T) {
	s := &ebookSetupStub{}
	r := ebookSetupRequest(http.MethodPost, "", "platform_admin", true)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", "100123")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()
	ebookSetupHandler(s).HandleEbookIntegrationTest(w, r)
	if w.Code != http.StatusOK || s.calls != 1 || s.tested != "100123" || s.v.UserID != 7 || !s.v.Admin || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("connection test: %d %s %+v", w.Code, w.Body.String(), s)
	}
}

func (s *ebookSetupStub) ResolvePlatformViewer(_ context.Context, id int) (bookrequests.Viewer, error) {
	s.platformCalls++
	return bookrequests.Viewer{UserID: id, OrganizationID: uuid.MustParse("00000000-0000-0000-0000-000000000025"), Admin: true}, s.platformError
}
func TestBloemEbookSetupPlatformContextUsesDefaultViewer(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		handle     func(*BloemBookRequestsHandler) http.HandlerFunc
		status     int
	}{
		{"list", "", func(h *BloemBookRequestsHandler) http.HandlerFunc { return h.HandleEbookIntegrationList }, 200},
		{"policy", `{"enabled":true}`, func(h *BloemBookRequestsHandler) http.HandlerFunc { return h.HandleEbookRequestPolicy }, 200},
		{"create", `{"name":"Books","base_url":"https://example.test","api_key":"synthetic","installation_id":1,"capability_id":"router"}`, func(h *BloemBookRequestsHandler) http.HandlerFunc { return h.HandleEbookIntegrationCreate }, 201},
		{"library", `{"library_id":25}`, func(h *BloemBookRequestsHandler) http.HandlerFunc { return h.HandleEbookIntegrationLibrary }, 200},
		{"test", "", func(h *BloemBookRequestsHandler) http.HandlerFunc { return h.HandleEbookIntegrationTest }, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ebookSetupStub{}
			h := ebookSetupHandler(s)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/organization/ebook-request-integrations", strings.NewReader(tc.body))
			r = r.WithContext(apimw.SetAdminContextClaims(r.Context(), auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}))
			tc.handle(h)(w, r)
			if w.Code != tc.status || s.calls != 1 || s.platformCalls != 1 {
				t.Fatalf("platform setup: %d %s calls=%d resolved=%d", w.Code, w.Body.String(), s.calls, s.platformCalls)
			}
		})
	}
}
func TestBloemEbookSetupPlatformContextFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims auth.AdminContextClaims
		err    error
	}{
		{"missing claims", auth.AdminContextClaims{}, nil},
		{"missing account", auth.AdminContextClaims{Scope: auth.AdminScopePlatform}, nil},
		{"revoked membership", auth.AdminContextClaims{Scope: auth.AdminScopePlatform, AccountID: 7}, bookrequests.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ebookSetupStub{platformError: tc.err}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/organization/ebook-request-policy", strings.NewReader(`{"enabled":true}`))
			r = r.WithContext(apimw.SetAdminContextClaims(r.Context(), tc.claims))
			ebookSetupHandler(s).HandleEbookRequestPolicy(w, r)
			if w.Code != 403 || s.calls != 0 {
				t.Fatalf("invalid context reached setup: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
