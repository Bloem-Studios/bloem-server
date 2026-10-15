package handlers

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
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
