package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// Security review follow-ups: Bookwarehouse base_url, insecure flags and
// case-variant keys are guarded in organization scope.
func TestNativeStorageOrganizationConnectionGuardHardening(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config map[string]map[string]any
		code   string
	}{
		{"bookwarehouse_public", map[string]map[string]any{"connection": {"base_url": "https://93.184.216.34/library", "api_key": "k"}}, ""},
		{"bookwarehouse_public_s3_port", map[string]map[string]any{"connection": {"base_url": "https://93.184.216.34:8443/"}}, ""},
		{"bookwarehouse_private", map[string]map[string]any{"connection": {"base_url": "https://10.0.0.140/x"}}, "s3_endpoint_rejected"},
		{"bookwarehouse_http", map[string]map[string]any{"connection": {"base_url": "http://93.184.216.34/x"}}, "s3_endpoint_rejected"},
		{"bookwarehouse_port", map[string]map[string]any{"connection": {"base_url": "https://93.184.216.34:8080/x"}}, "s3_endpoint_rejected"},
		{"bookwarehouse_metadata", map[string]map[string]any{"connection": {"base_url": "https://169.254.169.254/latest"}}, "s3_endpoint_rejected"},
		{"bookwarehouse_userinfo", map[string]map[string]any{"connection": {"base_url": "https://u:p@93.184.216.34/"}}, "s3_endpoint_rejected"},
		{"bookwarehouse_not_string", map[string]map[string]any{"connection": {"base_url": true}}, "invalid_request"},
		{"insecure_http", map[string]map[string]any{"connection": {"allow_insecure_http": true}}, "s3_endpoint_rejected"},
		{"insecure_http_string", map[string]map[string]any{"connection": {"allow_insecure_http": "true"}}, "s3_endpoint_rejected"},
		{"insecure_http_false", map[string]map[string]any{"connection": {"allow_insecure_http": false}}, ""},
		{"case_endpoint", map[string]map[string]any{"connection": {"Endpoint": "https://10.0.0.5"}}, "invalid_request"},
		{"case_endpoint_public", map[string]map[string]any{"connection": {"ENDPOINT": "https://93.184.216.34"}}, "invalid_request"},
		{"case_base_url", map[string]map[string]any{"connection": {"Base_URL": "https://10.0.0.5"}}, "invalid_request"},
		{"case_loopback", map[string]map[string]any{"connection": {"Allow_Loopback_HTTP": true}}, "invalid_request"},
		{"case_insecure", map[string]map[string]any{"connection": {"allow_Insecure_http": true}}, "invalid_request"},
		{"duplicate_case_keys", map[string]map[string]any{"connection": {"bucket": "a", "Bucket": "b"}}, "invalid_request"},
		{"case_connection_section", map[string]map[string]any{"Connection": {"endpoint": "https://10.0.0.5"}}, "invalid_request"},
		{"other_key_case_ok", map[string]map[string]any{"connection": {"Source_Name": "x"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopeOrganization, tc.config)
			if got := nativeCodeOf(err); got != tc.code {
				t.Fatalf("organization: code %q, want %q", got, tc.code)
			}
			if err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopePlatform, tc.config); err != nil {
				t.Fatalf("platform scope guarded: %v", err)
			}
		})
	}
}

// Every organization route that takes a configuration applies the guard to a
// Bookwarehouse base_url: install, discover, catalog install and replace.
func TestNativeStorageOrganizationRoutesGuardBaseURL(t *testing.T) {
	actor := auth.AdminContextClaims{AccountID: 3, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New()}
	withActor := func(r *http.Request) *http.Request {
		ctx := middleware.SetAdminContextClaims(r.Context(), actor)
		ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
		return r.WithContext(ctx)
	}
	const connection = `{"connection":{"base_url":"http://10.0.0.140:8080/x","allow_insecure_http":true,"api_key":"k"}}`
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)

	install := nativeMultipartRequest(t, [][2]string{{"request", `{"artifact_key":"bw","provider_source_id":"p","root_entry_id":"r","enabled":true,"config":` + connection + `}`}, {"binary", "bin"}})
	w := httptest.NewRecorder()
	h.HandleInstall(w, withActor(install))
	assertNativeError(t, w, 422, "s3_endpoint_rejected")

	discover := nativeMultipartRequest(t, [][2]string{{"request", `{"artifact_key":"bw","provider_source_id":"p","enabled":true,"config":` + connection + `}`}, {"binary", "bin"}})
	w = httptest.NewRecorder()
	h.HandleDiscoverInstall(w, withActor(discover))
	assertNativeError(t, w, 422, "s3_endpoint_rejected")

	catalogInstall := httptest.NewRequest("POST", "/catalog/installations", strings.NewReader(`{"artifact_key":"bookwarehouse-1.0.0-linux-amd64","provider_source_id":"p","root_entry_id":"r","enabled":true,"config":`+connection+`}`))
	catalogInstall.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.HandleCatalogInstall(w, withActor(catalogInstall))
	assertNativeError(t, w, 422, "s3_endpoint_rejected")

	replace := httptest.NewRequest("PUT", "/sources/x/configuration", strings.NewReader(`{"expected_revision":2,"config":`+connection+`}`))
	replace.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.HandleReplaceConfiguration(w, mux(withActor(replace), "source_key", "00000000-0000-0000-0000-000000000001"))
	assertNativeError(t, w, 422, "s3_endpoint_rejected")
}

func TestCatalogDiscoverRateLimitedPerActor(t *testing.T) {
	const body = `{"artifact_key":"s3-0.3.2-linux-amd64-x","provider_source_id":"s3_abc","enabled":true,"config":{"connection":{"bucket":"media"}}}`
	h := NewBloemNativeStorageManagementHandler(nil)
	h.CatalogDiscoverLimit = nativestorage.NewActorRateLimiter(10, 3)
	org := h.ForAdminScope(auth.AdminScopeOrganization)
	for i := 0; i < 3; i++ {
		r, _ := catalogDiscoverOrgRequest(t, body)
		w := httptest.NewRecorder()
		org.HandleCatalogDiscoverInstall(w, r)
		// Past the limiter; Sources is nil, so the service answers unavailable.
		assertNativeError(t, w, http.StatusServiceUnavailable, "native_storage_unavailable")
		if w.Header().Get("Retry-After") != "" {
			t.Fatalf("request %d was rate limited", i)
		}
	}
	// catalogDiscoverOrgRequest uses account 1 for every request.
	r, _ := catalogDiscoverOrgRequest(t, body)
	w := httptest.NewRecorder()
	org.HandleCatalogDiscoverInstall(w, r)
	assertNativeError(t, w, http.StatusServiceUnavailable, "native_storage_unavailable")
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("fourth discovery within the burst was not rate limited")
	}
}

// A key that is not an admitted S3 catalog release is refused before any
// download: the zero registry has no records, and reaching CatalogBinary would
// answer differently.
func TestCatalogDiscoverRefusesUnadmittedKeyBeforeDownload(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(&nativestorage.SourceManagement{})
	h.Registry = &plugins.NativeStorageRegistry{}
	r, _ := catalogDiscoverOrgRequest(t, `{"artifact_key":"bookwarehouse-1.0.0-linux-amd64","provider_source_id":"s3_abc","enabled":true,"config":{"connection":{"bucket":"media"}}}`)
	w := httptest.NewRecorder()
	h.ForAdminScope(auth.AdminScopeOrganization).HandleCatalogDiscoverInstall(w, r)
	assertNativeError(t, w, 422, "artifact_rejected")

	artifacts := []plugins.NativeStorageArtifactView{
		{ArtifactKey: "s3-catalog", PluginID: nativeStorageS3PluginID, Catalog: true},
		{ArtifactKey: "s3-manual", PluginID: nativeStorageS3PluginID, Catalog: false},
		{ArtifactKey: "bw-catalog", PluginID: "bloem.storage.bookwarehouse", Catalog: true},
	}
	for key, want := range map[string]bool{"s3-catalog": true, "s3-manual": false, "bw-catalog": false, "unknown": false} {
		if got := nativeStorageCatalogDiscoverKeyAdmitted(artifacts, key); got != want {
			t.Fatalf("%s admitted=%v want %v", key, got, want)
		}
	}
}

func TestS3BrowseHandlerMarksPlatformActors(t *testing.T) {
	if !nativeStorageS3Options(browsePlatformActor).Platform || nativeStorageS3Options(browseOrgActor).Platform {
		t.Fatal("only platform actors may use the reserved slot")
	}
}
