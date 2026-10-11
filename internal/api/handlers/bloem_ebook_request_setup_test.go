package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type ebookSetupStub struct {
	calls   int
	v       bookrequests.Viewer
	enabled bool
	in      EbookRequestConnectionInput
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
		for _, handle := range []http.HandlerFunc{h.HandleEbookRequestPolicy, h.HandleEbookIntegrationCreate, h.HandleEbookIntegrationLibrary} {
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
