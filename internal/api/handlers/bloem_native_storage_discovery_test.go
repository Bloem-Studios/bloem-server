package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestNativeStorageDiscoveryCapabilityRequiresComposition(t *testing.T) {
	h := NewBloemNativeStorageManagementHandler(nil)
	body := h.nativeStorageCapabilities(t.Context())
	if body["s3_initial_root_discovery"] != false {
		t.Fatalf("uncomposed discovery capability = %v, want false", body["s3_initial_root_discovery"])
	}
	if body["backend_verified"] != false || body["schema"] != 2 {
		t.Fatal("discovery changed management schema or backend certification")
	}
}

func TestNativeStorageDiscoveryMultipartRequest(t *testing.T) {
	const valid = `{"artifact_key":"approved-s3","provider_source_id":" source/../opaque ","enabled":false,"config":{"connection":{"bucket":"fixture"}}}`
	for _, scope := range []auth.AdminScope{auth.AdminScopePlatform, auth.AdminScopeOrganization} {
		cmd, fields, err := nativeStorageDiscoverInstallBody(httptest.NewRecorder(), nativeMultipartRequest(t, [][2]string{{"request", valid}, {"binary", "approved-binary"}}))
		if err == nil {
			err = nativeStorageDiscoverInstallRequest(&cmd, fields, scope)
		}
		if err != nil {
			t.Fatalf("%s valid discovery rejected: %v", scope, err)
		}
		if cmd.ProviderSourceID != " source/../opaque " || cmd.Enabled || string(cmd.Binary) != "approved-binary" {
			t.Fatal("opaque identity, disabled state or binary changed")
		}
		if cmd.Config["connection"]["bucket"] != "fixture" {
			t.Fatal("complete configuration was lost")
		}
	}
	for _, name := range []string{"root_entry_id", "source_key", "expected_revision"} {
		for _, value := range []string{"null", `"caller-selected"`} {
			t.Run(name+value, func(t *testing.T) {
				body := strings.TrimSuffix(valid, "}") + `,"` + name + `":` + value + `}`
				cmd, fields, err := nativeStorageDiscoverInstallBody(httptest.NewRecorder(), nativeMultipartRequest(t, [][2]string{{"request", body}, {"binary", "bin"}}))
				if err == nil || len(cmd.Binary) != 0 || fields != nil {
					t.Fatal("caller root, retained identity or revision was accepted")
				}
				w := httptest.NewRecorder()
				writeNativeStorageError(w, err, true)
				assertNativeError(t, w, 400, "invalid_request")
			})
		}
	}
}

func TestNativeStorageDiscoveryRequiredAndScopeFields(t *testing.T) {
	const valid = `{"artifact_key":"approved-s3","provider_source_id":"opaque","enabled":false,"config":{}}`
	for _, required := range []string{"artifact_key", "provider_source_id", "enabled", "config"} {
		for _, remove := range []bool{false, true} {
			variant := "null"
			if remove {
				variant = "missing"
			}
			t.Run(required+"_"+variant, func(t *testing.T) {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(valid), &fields); err != nil {
					t.Fatal(err)
				}
				fields[required] = json.RawMessage(`null`)
				if remove {
					delete(fields, required)
				}
				body, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				var cmd nativestorage.DiscoverInstallCommand
				decoded, err := nativeStorageDecodeJSON(body, &cmd)
				if err == nil {
					err = nativeStorageDiscoverInstallRequest(&cmd, decoded, auth.AdminScopePlatform)
				}
				if err == nil {
					t.Fatalf("%s missing/null field accepted", required)
				}
			})
		}
	}
	for _, tc := range []struct {
		name, extra string
		scope       auth.AdminScope
		valid       bool
	}{
		{"platform_null", `,"organization_id":null`, auth.AdminScopePlatform, true},
		{"platform_organization", `,"organization_id":"00000000-0000-0000-0000-000000000001"`, auth.AdminScopePlatform, true},
		{"platform_zero", `,"organization_id":"00000000-0000-0000-0000-000000000000"`, auth.AdminScopePlatform, false},
		{"organization_null", `,"organization_id":null`, auth.AdminScopeOrganization, false},
		{"organization_target", `,"organization_id":"00000000-0000-0000-0000-000000000001"`, auth.AdminScopeOrganization, false},
		{"duplicate_source", `,"provider_source_id":"different"`, auth.AdminScopePlatform, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.TrimSuffix(valid, "}") + tc.extra + `}`
			var cmd nativestorage.DiscoverInstallCommand
			fields, err := nativeStorageDecodeJSON([]byte(body), &cmd)
			if err == nil {
				err = nativeStorageDiscoverInstallRequest(&cmd, fields, tc.scope)
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v want=%v", err == nil, tc.valid)
			}
		})
	}
	var old nativestorage.InstallCommand
	fields, err := nativeStorageDecodeJSON([]byte(valid), &old)
	if err == nil {
		err = nativeStorageInstallRequest(&old, fields, auth.AdminScopePlatform)
	}
	if err == nil {
		t.Fatal("existing installation no longer requires an explicit root")
	}
}

func TestNativeStorageDiscoveryActorPrecedesBody(t *testing.T) {
	for _, scope := range []auth.AdminScope{auth.AdminScopePlatform, auth.AdminScopeOrganization} {
		h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(scope)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/installations/discover", nil)
		r.Body = nativeUnreadBody{t}
		h.HandleDiscoverInstall(w, r)
		assertNativeError(t, w, http.StatusUnauthorized, "tenant_session_required")
	}
	actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New()}
	for _, tc := range []struct {
		name   string
		scope  auth.AdminScope
		tenant *tenancy.Context
		code   string
	}{
		{"organization_on_platform", auth.AdminScopePlatform, nil, "insufficient_platform_authority"},
		{"organization_without_tenant", auth.AdminScopeOrganization, nil, "insufficient_organization_authority"},
		{"organization_mismatched_tenant", auth.AdminScopeOrganization, &tenancy.Context{AccountID: actor.AccountID, OrganizationID: uuid.New(), MembershipID: actor.MembershipID}, "insufficient_organization_authority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(tc.scope)
			r := httptest.NewRequest("POST", "/installations/discover", nil)
			ctx := middleware.SetAdminContextClaims(r.Context(), actor)
			if tc.tenant != nil {
				ctx = tenancy.WithContext(ctx, *tc.tenant)
			}
			r = r.WithContext(ctx)
			r.Body = nativeUnreadBody{t}
			w := httptest.NewRecorder()
			h.HandleDiscoverInstall(w, r)
			assertNativeError(t, w, http.StatusForbidden, tc.code)
		})
	}
}

func TestNativeStorageDiscoveryHandlerStrictRequestBeforeService(t *testing.T) {
	const valid = `{"artifact_key":"approved-s3","provider_source_id":"opaque","enabled":true,"config":{}}`
	for _, tc := range []struct {
		name, body, query string
		organization      bool
		status            int
		code              string
	}{
		{"unavailable", valid, "", false, 503, "native_storage_unavailable"},
		{"root_null", strings.TrimSuffix(valid, "}") + `,"root_entry_id":null}`, "", false, 400, "invalid_request"},
		{"query", valid, "?root_entry_id=caller", false, 400, "invalid_request"},
		{"organization_unavailable", valid, "", true, 503, "native_storage_unavailable"},
		{"organization_target_null", strings.TrimSuffix(valid, "}") + `,"organization_id":null}`, "", true, 400, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopePlatform}
			if tc.organization {
				actor.Scope = auth.AdminScopeOrganization
				actor.OrganizationID = uuid.New()
				actor.MembershipID = uuid.New()
			}
			h := NewBloemNativeStorageManagementHandler(nil).ForAdminScope(actor.Scope)
			r := nativeMultipartRequest(t, [][2]string{{"request", tc.body}, {"binary", "bin"}})
			r.URL.RawQuery = strings.TrimPrefix(tc.query, "?")
			ctx := middleware.SetAdminContextClaims(r.Context(), actor)
			if tc.organization {
				ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: actor.AccountID, OrganizationID: actor.OrganizationID, MembershipID: actor.MembershipID})
			}
			r = r.WithContext(ctx)
			w := httptest.NewRecorder()
			h.HandleDiscoverInstall(w, r)
			assertNativeError(t, w, tc.status, tc.code)
		})
	}
}

type catalogDiscoveryCapabilityFixture struct{}

func (catalogDiscoveryCapabilityFixture) NativeStorageReady(context.Context) bool        { return true }
func (catalogDiscoveryCapabilityFixture) InitialRootDiscoveryReady(context.Context) bool { return true }

// The catalog discovery route is platform-only, so the capability must not be
// advertised in organization scope even when everything else is composed.
func TestNativeStorageCatalogDiscoveryCapabilityIsPlatformScoped(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scope    auth.AdminScope
		registry *plugins.NativeStorageRegistry
		want     bool
	}{
		{"platform_composed", auth.AdminScopePlatform, &plugins.NativeStorageRegistry{}, true},
		{"platform_without_registry", auth.AdminScopePlatform, nil, false},
		{"organization_composed", auth.AdminScopeOrganization, &plugins.NativeStorageRegistry{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &BloemNativeStorageManagementHandler{Capabilities: catalogDiscoveryCapabilityFixture{}, Registry: tc.registry}
			h = h.ForAdminScope(tc.scope)
			body := h.nativeStorageCapabilities(t.Context())
			if body["s3_catalog_discovery"] != tc.want {
				t.Fatalf("s3_catalog_discovery = %v, want %v", body["s3_catalog_discovery"], tc.want)
			}
			if body["s3_initial_root_discovery"] != true || body["credential_rotation_in_use"] != true {
				t.Fatalf("scope changed unrelated capabilities: %+v", body)
			}
		})
	}
}
