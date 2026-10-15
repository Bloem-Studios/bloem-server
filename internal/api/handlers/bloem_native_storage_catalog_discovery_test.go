package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func asNativeError(err error, target **catalog.NativeOnboardingError) bool {
	return errors.As(err, target)
}

func catalogDiscoverFields(extra map[string]string) map[string]json.RawMessage {
	f := map[string]json.RawMessage{"artifact_key": json.RawMessage(`"s3-0.3.2-linux-amd64-x"`), "provider_source_id": json.RawMessage(`"s3_abc"`),
		"enabled": json.RawMessage(`true`), "config": json.RawMessage(`{"connection":{"bucket":"media"}}`)}
	for k, v := range extra {
		f[k] = json.RawMessage(v)
	}
	return f
}

// Platform and organization administrators may both discover an S3 source from
// the catalog; neither may name an owner (organization_id), even as null.
func TestCatalogDiscoverRequestBothScopes(t *testing.T) {
	for _, scope := range []auth.AdminScope{auth.AdminScopePlatform, auth.AdminScopeOrganization} {
		t.Run(string(scope), func(t *testing.T) {
			cmd := nativestorage.DiscoverInstallCommand{ArtifactKey: "s3-0.3.2-linux-amd64-x", ProviderSourceID: "s3_abc", Enabled: true,
				Config: map[string]map[string]any{"connection": {"bucket": "media"}}}
			if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), scope); err != nil {
				t.Fatalf("%s request rejected: %v", scope, err)
			}
			for _, value := range []string{"null", `"00000000-0000-0000-0000-000000000001"`} {
				err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"organization_id": value}), scope)
				var typed *catalog.NativeOnboardingError
				if !asNativeError(err, &typed) || typed.Code != "invalid_request" {
					t.Fatalf("organization_id %s must be invalid_request, got %v", value, err)
				}
			}
			if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"root_entry_id": `"x"`}), scope); err == nil {
				t.Fatal("unknown/explicit root field accepted")
			}
			missing := catalogDiscoverFields(nil)
			delete(missing, "config")
			if err := nativeStorageCatalogDiscoverRequest(&cmd, missing, scope); err == nil {
				t.Fatal("missing config accepted")
			}
		})
	}
}

func TestCatalogDiscoverRequestRejectsStatePath(t *testing.T) {
	cmd := nativestorage.DiscoverInstallCommand{ArtifactKey: "k", ProviderSourceID: "p", Enabled: true, Config: map[string]map[string]any{"connection": {"state_path": ""}}}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopePlatform); err == nil {
		t.Fatal("state_path accepted")
	}
}

func TestCatalogDiscoverCommandRequiresAdminContext(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopePlatform)
	w := httptest.NewRecorder()
	h.HandleCatalogDiscoverInstall(w, httptest.NewRequest("POST", "/catalog/installations/discover", strings.NewReader(`{}`)))
	if w.Code != 401 {
		t.Fatalf("anonymous catalog discover status %d", w.Code)
	}
}

func catalogDiscoverOrgRequest(t *testing.T, body string) (*http.Request, auth.AdminContextClaims) {
	t.Helper()
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New()}
	r := httptest.NewRequest("POST", "/catalog/installations/discover", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetAdminContextClaims(r.Context(), actor)
	ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
	return r.WithContext(ctx), actor
}

// Organization scope now reaches the strict body checks and, with a valid
// body, the source service (nil here, so 503) that re-establishes ownership.
func TestCatalogDiscoverOrganizationScopeIsServed(t *testing.T) {
	const valid = `{"artifact_key":"s3-0.3.2-linux-amd64-x","provider_source_id":"s3_abc","enabled":true,"config":{"connection":{"bucket":"media","endpoint":"https://93.184.216.34:9000"}}}`
	for _, tc := range []struct {
		name, body string
		status     int
		code       string
	}{
		{"valid_reaches_service", valid, http.StatusServiceUnavailable, "native_storage_unavailable"},
		{"malformed", `{"artifact_key":`, http.StatusBadRequest, "invalid_request"},
		{"organization_id_null", strings.TrimSuffix(valid, "}") + `,"organization_id":null}`, http.StatusBadRequest, "invalid_request"},
		{"organization_id_other", strings.TrimSuffix(valid, "}") + `,"organization_id":"00000000-0000-0000-0000-000000000001"}`, http.StatusBadRequest, "invalid_request"},
		{"state_path", strings.Replace(valid, `"bucket":"media"`, `"bucket":"media","state_path":"/tmp/x"`, 1), http.StatusBadRequest, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)
			r, _ := catalogDiscoverOrgRequest(t, tc.body)
			w := httptest.NewRecorder()
			h.HandleCatalogDiscoverInstall(w, r)
			assertNativeError(t, w, tc.status, tc.code)
		})
	}
}

// An organization administrator cannot aim the plugin process at an internal
// address: the configured endpoint passes the browse guard first. Platform
// administrators keep the plugin's own endpoint rules.
func TestNativeStorageOrganizationConnectionGuard(t *testing.T) {
	config := func(connection map[string]any) map[string]map[string]any {
		return map[string]map[string]any{"connection": connection}
	}
	for _, tc := range []struct {
		name       string
		connection map[string]any
		code       string
	}{
		{"no_endpoint", map[string]any{"bucket": "media"}, ""},
		{"null_endpoint", map[string]any{"endpoint": nil}, ""},
		{"empty_endpoint", map[string]any{"endpoint": ""}, ""},
		{"public_literal", map[string]any{"endpoint": "https://93.184.216.34"}, ""},
		{"public_literal_s3_port", map[string]any{"endpoint": "https://93.184.216.34:7480"}, ""},
		{"loopback", map[string]any{"endpoint": "https://127.0.0.1"}, "s3_endpoint_rejected"},
		{"metadata", map[string]any{"endpoint": "https://169.254.169.254"}, "s3_endpoint_rejected"},
		{"private", map[string]any{"endpoint": "https://10.1.2.3:9000"}, "s3_endpoint_rejected"},
		{"mapped", map[string]any{"endpoint": "https://[::ffff:192.168.1.1]"}, "s3_endpoint_rejected"},
		{"http", map[string]any{"endpoint": "http://93.184.216.34"}, "s3_endpoint_rejected"},
		{"port", map[string]any{"endpoint": "https://93.184.216.34:6379"}, "s3_endpoint_rejected"},
		{"loopback_http_switch", map[string]any{"allow_loopback_http": true}, "s3_endpoint_rejected"},
		{"not_string", map[string]any{"endpoint": 7.0}, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopeOrganization, config(tc.connection))
			if got := nativeCodeOf(err); got != tc.code {
				t.Fatalf("organization: code %q, want %q", got, tc.code)
			}
			if err := nativeStorageOrganizationConnectionGuard(t.Context(), auth.AdminScopePlatform, config(tc.connection)); err != nil {
				t.Fatalf("platform scope guarded: %v", err)
			}
		})
	}
	// Every organization route that accepts a configuration applies it before the service.
	body := `{"artifact_key":"s3-0.3.2-linux-amd64-x","provider_source_id":"s3_abc","enabled":true,"config":{"connection":{"endpoint":"https://169.254.169.254"}}}`
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)
	r, _ := catalogDiscoverOrgRequest(t, body)
	w := httptest.NewRecorder()
	h.HandleCatalogDiscoverInstall(w, r)
	assertNativeError(t, w, 422, "s3_endpoint_rejected")
	r, _ = catalogDiscoverOrgRequest(t, `{"expected_revision":3,"config":{"connection":{"endpoint":"https://10.0.0.1"}}}`)
	r = mux(r, "source_key", "00000000-0000-0000-0000-000000000001")
	w = httptest.NewRecorder()
	h.HandleReplaceConfiguration(w, r)
	assertNativeError(t, w, 422, "s3_endpoint_rejected")
}

func nativeCodeOf(err error) string {
	var typed *catalog.NativeOnboardingError
	if errors.As(err, &typed) && typed != nil {
		return typed.Code
	}
	return ""
}

func mux(r *http.Request, key, value string) *http.Request {
	route := chi.NewRouteContext()
	route.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
}

// Platform scope keeps the strict body errors: only the organization check moved.
func TestCatalogDiscoverPlatformScopeStillRejectsMalformedBody(t *testing.T) {
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopePlatform}
	h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopePlatform)
	r := httptest.NewRequest("POST", "/catalog/installations/discover", strings.NewReader(`{"artifact_key":`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleCatalogDiscoverInstall(w, r.WithContext(middleware.SetAdminContextClaims(r.Context(), actor)))
	assertNativeError(t, w, http.StatusBadRequest, "invalid_request")
}
