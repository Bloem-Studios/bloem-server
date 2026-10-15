package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	scopeTestS3Key            = "s3-0.3.2-linux-amd64-5af4d564c74df1500ede6f0de2ef319657bc55c903fd80f85a5ec1002ca7148d"
	scopeTestBookwarehouseKey = "bookwarehouse-0.3.0-linux-amd64-0000000000000000000000000000000000000000000000000000000000000000"
)

func catalogScopeRequest(t *testing.T, scope auth.AdminScope, path, body string) *http.Request {
	t.Helper()
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: scope}
	if scope == auth.AdminScopeOrganization {
		actor.OrganizationID, actor.MembershipID = uuid.New(), uuid.New()
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := middleware.SetAdminContextClaims(r.Context(), actor)
	if scope == auth.AdminScopeOrganization {
		ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
	}
	return r.WithContext(ctx)
}

func catalogInstallBody(key string) string {
	return `{"artifact_key":"` + key + `","provider_source_id":"s3_abc","root_entry_id":"caller-root","enabled":true,"config":{"connection":{"bucket":"media"}}}`
}

func catalogUpgradeBody(key string) string {
	return `{"artifact_key":"` + key + `","expected_revision":1,"source_key":"` + uuid.NewString() + `"}`
}

// Sources and Registry are nil in these handlers, so a request that passed the
// S3 check would answer 503 native_storage_unavailable before CatalogBinary;
// a 404 or 422 proves the refusal happened earlier, with no download.
func TestCatalogInstallRefusesS3KeysBeforeDownload(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scope  auth.AdminScope
		key    string
		status int
		code   string
	}{
		{"organization_s3", auth.AdminScopeOrganization, scopeTestS3Key, http.StatusNotFound, "not_found"},
		{"platform_s3_must_discover", auth.AdminScopePlatform, scopeTestS3Key, http.StatusUnprocessableEntity, "artifact_rejected"},
		{"organization_bookwarehouse_unchanged", auth.AdminScopeOrganization, scopeTestBookwarehouseKey, http.StatusServiceUnavailable, nativeStorageUnavailableCode},
		{"organization_unknown_key_unchanged", auth.AdminScopeOrganization, "unknown-artifact", http.StatusServiceUnavailable, nativeStorageUnavailableCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(tc.scope)
			w := httptest.NewRecorder()
			h.HandleCatalogInstall(w, catalogScopeRequest(t, tc.scope, "/catalog/installations", catalogInstallBody(tc.key)))
			assertNativeError(t, w, tc.status, tc.code)
		})
	}
}

func TestCatalogUpgradeRefusesS3KeysForOrganizationsBeforeDownload(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scope  auth.AdminScope
		key    string
		status int
		code   string
	}{
		{"organization_s3", auth.AdminScopeOrganization, scopeTestS3Key, http.StatusNotFound, "not_found"},
		{"platform_s3_unchanged", auth.AdminScopePlatform, scopeTestS3Key, http.StatusServiceUnavailable, nativeStorageUnavailableCode},
		{"organization_bookwarehouse_unchanged", auth.AdminScopeOrganization, scopeTestBookwarehouseKey, http.StatusServiceUnavailable, nativeStorageUnavailableCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(tc.scope)
			r := catalogScopeRequest(t, tc.scope, "/catalog/installations/7/upgrade", catalogUpgradeBody(tc.key))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("installation_id", "7")
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
			w := httptest.NewRecorder()
			h.HandleCatalogUpgrade(w, r)
			assertNativeError(t, w, tc.status, tc.code)
		})
	}
}

func TestNativeStorageCatalogPluginIDUsesApprovedArtifactsThenKeyPrefix(t *testing.T) {
	artifacts := []plugins.NativeStorageArtifactView{{ArtifactKey: "static-approved-s3", PluginID: "bloem.storage.s3"}}
	for key, want := range map[string]string{
		"static-approved-s3":      "bloem.storage.s3",
		scopeTestS3Key:            "bloem.storage.s3", // not yet fetched into the catalog
		scopeTestBookwarehouseKey: "bloem.storage.bookwarehouse",
		"unknown-artifact":        "",
		"s3":                      "",
	} {
		if got := nativeStorageCatalogPluginID(artifacts, key); got != want {
			t.Errorf("plugin id for %q = %q, want %q", key, got, want)
		}
	}
}

func TestNativeStorageCatalogKeyAllowed(t *testing.T) {
	code := func(err error) string {
		var typed *catalog.NativeOnboardingError
		if err == nil {
			return ""
		}
		if !asNativeError(err, &typed) {
			t.Fatalf("untyped error %v", err)
		}
		return typed.Code
	}
	for _, tc := range []struct {
		plugin  string
		scope   auth.AdminScope
		install bool
		want    string
	}{
		{"bloem.storage.s3", auth.AdminScopeOrganization, true, "not_found"},
		{"bloem.storage.s3", auth.AdminScopeOrganization, false, "not_found"},
		{"bloem.storage.s3", auth.AdminScopePlatform, true, "artifact_rejected"},
		{"bloem.storage.s3", auth.AdminScopePlatform, false, ""},
		{"bloem.storage.bookwarehouse", auth.AdminScopeOrganization, true, ""},
		{"bloem.storage.bookwarehouse", auth.AdminScopeOrganization, false, ""},
		{"", auth.AdminScopeOrganization, true, ""},
	} {
		if got := code(nativeStorageCatalogKeyAllowed(tc.plugin, tc.scope, tc.install)); got != tc.want {
			t.Errorf("%s scope=%s install=%v: got %q want %q", tc.plugin, tc.scope, tc.install, got, tc.want)
		}
	}
}
