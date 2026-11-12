package handlers

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// An organization administrator cannot route a storage plugin through a proxy:
// the plugin would connect to whatever host and port http_proxy names. Only a
// platform administrator may set the proxy fields, in any spelling the
// plugin's case-insensitive JSON decoding accepts, with any value.
func TestNativeStorageOrganizationProxyGuard(t *testing.T) {
	config := func(connection map[string]any) map[string]map[string]any {
		return map[string]map[string]any{"connection": connection}
	}
	for _, tc := range []struct {
		name       string
		connection map[string]any
		field      string
	}{
		{"proxy_internal", map[string]any{"http_proxy": "http://192.0.2.5:6379"}, "http_proxy"},
		{"proxy_public", map[string]any{"http_proxy": "http://93.184.216.34:3128"}, "http_proxy"},
		{"proxy_empty", map[string]any{"http_proxy": ""}, "http_proxy"},
		{"proxy_null", map[string]any{"http_proxy": nil}, "http_proxy"},
		{"proxy_not_string", map[string]any{"http_proxy": 7.0}, "http_proxy"},
		{"proxy_username", map[string]any{"http_proxy_username": "u"}, "http_proxy_username"},
		{"proxy_password", map[string]any{"http_proxy_password": "p"}, "http_proxy_password"},
		{"proxy_upper", map[string]any{"HTTP_PROXY": "http://192.0.2.5:3128"}, "http_proxy"},
		{"proxy_mixed", map[string]any{"Http_Proxy": "http://192.0.2.5:3128"}, "http_proxy"},
		// encoding/json folds U+017F (long s) to "s" and U+212A (Kelvin) to "k".
		{"username_long_s", map[string]any{"http_proxy_uſername": "u"}, "http_proxy_username"},
		{"password_long_s", map[string]any{"http_proxy_paſſword": "p"}, "http_proxy_password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopeOrganization, config(tc.connection))
			var typed *catalog.NativeOnboardingError
			if !errors.As(err, &typed) || typed.Code != "platform_setting_required" || typed.Field != "connection."+tc.field {
				t.Fatalf("organization: got %#v, want platform_setting_required for connection.%s", err, tc.field)
			}
			apiErr := NativeStorageAPIError(err)
			if apiErr.Status != 403 || apiErr.Code != "platform_setting_required" || !strings.Contains(apiErr.Message, "connection."+tc.field) {
				t.Fatalf("API error %+v does not name connection.%s with 403", apiErr, tc.field)
			}
			if err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopePlatform, config(tc.connection)); err != nil {
				t.Fatalf("platform scope guarded: %v", err)
			}
		})
	}
	// Limits are not connection targets: an organization administrator may set them.
	limits := map[string]any{"auth_failure_budget": 3.0, "auth_failure_window_seconds": 120.0, "auth_pause_seconds": 300.0, "auth_pause_max_seconds": 1800.0,
		"max_concurrent_requests": 16.0, "max_listing_requests": 3.0, "max_read_mbps": 90.0}
	if err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopeOrganization, config(limits)); err != nil {
		t.Fatalf("organization limits refused: %v", err)
	}
}

// The guarded endpoint keys match the way encoding/json matches field names,
// which folds more than strings.ToLower: a long s still names base_url.
func TestNativeStorageOrganizationGuardFoldsLikeJSON(t *testing.T) {
	for name, connection := range map[string]map[string]any{
		"base_url_long_s":  {"baſe_url": "https://192.0.2.5/x"},
		"insecure_long_s":  {"allow_inſecure_http": true},
		"loopback_kelvin":  {"allow_loopbacK_http": true},
		"duplicate_long_s": {"bucket": "a", "bucketſ": "b", "buckets": "c"},
		"duplicate_case":   {"bucket": "a", "Bucket": "b"},
	} {
		t.Run(name, func(t *testing.T) {
			err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopeOrganization, map[string]map[string]any{"connection": connection})
			if got := nativeCodeOf(err); got != "invalid_request" {
				t.Fatalf("code %q, want invalid_request", got)
			}
		})
	}
}

// Every organization route that takes a configuration refuses the proxy with
// 403 before the service, and the body names the field.
func TestNativeStorageOrganizationRoutesRefuseProxy(t *testing.T) {
	const connection = `{"connection":{"bucket":"media","http_proxy":"http://192.0.2.140:3128"}}`
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)

	r, _ := catalogDiscoverOrgRequest(t, `{"artifact_key":"s3-0.5.0-linux-amd64-x","provider_source_id":"s3_abc","enabled":true,"config":`+connection+`}`)
	w := httptest.NewRecorder()
	h.HandleCatalogDiscoverInstall(w, r)
	assertProxyRefused(t, w)

	r, _ = catalogDiscoverOrgRequest(t, `{"expected_revision":3,"config":`+connection+`}`)
	w = httptest.NewRecorder()
	h.HandleReplaceConfiguration(w, mux(r, "source_key", "00000000-0000-0000-0000-000000000001"))
	assertProxyRefused(t, w)
}

func assertProxyRefused(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assertNativeError(t, w, 403, "platform_setting_required")
	var body struct{ Message string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !strings.Contains(body.Message, "connection.http_proxy") {
		t.Fatalf("message does not name connection.http_proxy: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "192.0.2.140") {
		t.Fatalf("refusal quoted the submitted proxy: %s", w.Body.String())
	}
}
