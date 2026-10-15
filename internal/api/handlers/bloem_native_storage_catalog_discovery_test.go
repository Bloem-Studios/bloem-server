package handlers

import (
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

func TestCatalogDiscoverRequestPlatformOnly(t *testing.T) {
	cmd := nativestorage.DiscoverInstallCommand{ArtifactKey: "s3-0.3.2-linux-amd64-x", ProviderSourceID: "s3_abc", Enabled: true,
		Config: map[string]map[string]any{"connection": {"bucket": "media"}}}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopePlatform); err != nil {
		t.Fatalf("platform request rejected: %v", err)
	}
	err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopeOrganization)
	var typed *catalog.NativeOnboardingError
	if !asNativeError(err, &typed) || typed.Code != "not_found" {
		t.Fatalf("organization scope must be not_found, got %v", err)
	}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"organization_id": "null"}), auth.AdminScopePlatform); err == nil {
		t.Fatal("organization_id must be rejected even when null")
	}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"root_entry_id": `"x"`}), auth.AdminScopePlatform); err == nil {
		t.Fatal("unknown/explicit root field accepted")
	}
	missing := catalogDiscoverFields(nil)
	delete(missing, "config")
	if err := nativeStorageCatalogDiscoverRequest(&cmd, missing, auth.AdminScopePlatform); err == nil {
		t.Fatal("missing config accepted")
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

// Organization scope must 404 before the body is read or decoded, so a
// malformed, mistyped or oversized body from an organization admin reveals
// nothing beyond the route not existing for them. Sources and Registry are nil
// here: any path that got past the scope check would answer 503
// (native_storage_unavailable), never 404, and the unread body fails the test
// if it is touched.
func TestCatalogDiscoverOrganizationScopeIsNotFoundBeforeBody(t *testing.T) {
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New()}
	for _, tc := range []struct {
		name        string
		query       string
		contentType string
		length      int64
	}{
		{"unread_json_body", "", "application/json", -1},
		{"wrong_content_type", "", "text/plain", -1},
		{"oversized_declared_length", "", "application/json", nativeStorageJSONLimit + 1},
		{"unknown_query", "?root_entry_id=caller", "application/json", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)
			r := httptest.NewRequest("POST", "/catalog/installations/discover"+tc.query, nil)
			r.Header.Set("Content-Type", tc.contentType)
			r.Body = nativeUnreadBody{t}
			r.ContentLength = tc.length
			ctx := middleware.SetAdminContextClaims(r.Context(), actor)
			ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
			w := httptest.NewRecorder()
			h.HandleCatalogDiscoverInstall(w, r.WithContext(ctx))
			assertNativeError(t, w, http.StatusNotFound, "not_found")
		})
	}
	t.Run("malformed_json_body", func(t *testing.T) {
		h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(auth.AdminScopeOrganization)
		r := httptest.NewRequest("POST", "/catalog/installations/discover", strings.NewReader(`{"artifact_key":`))
		r.Header.Set("Content-Type", "application/json")
		ctx := middleware.SetAdminContextClaims(r.Context(), actor)
		ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
		w := httptest.NewRecorder()
		h.HandleCatalogDiscoverInstall(w, r.WithContext(ctx))
		assertNativeError(t, w, http.StatusNotFound, "not_found")
	})
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
