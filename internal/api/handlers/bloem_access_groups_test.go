package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

var accessGroupHandlerOrganizationID = uuid.MustParse("10000000-0000-0000-0000-000000000001")

func TestAccessGroupHandlerUsesOnlyValidatedTenantContext(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	handler := NewAccessGroupHandler(store)
	foreignOrganizationID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	// The v2 service takes no organization from the caller; only the tenant
	// the request context validated reaches the store.
	req := accessGroupRequestWithTenant(httptest.NewRequest(http.MethodPost, "/api/v2/admin/access-groups", nil))
	req.Header.Set("X-Organization-ID", foreignOrganizationID.String())
	if _, err := handler.CreateAdminAccessGroup(req.Context(), access.CreateGroupInput{Name: "Tenant Bound"}); err != nil {
		t.Fatalf("CreateAdminAccessGroup: %v", err)
	}
	if store.lastOrganizationID != accessGroupHandlerOrganizationID {
		t.Fatalf("store organization = %s, want validated tenant %s", store.lastOrganizationID, accessGroupHandlerOrganizationID)
	}
	if store.lastOrganizationID == foreignOrganizationID {
		t.Fatal("request-selected organization reached store")
	}
}

func accessGroupRequestWithTenant(req *http.Request) *http.Request {
	tenant := tenancy.Context{
		OrganizationID: accessGroupHandlerOrganizationID,
		AccountID:      1,
		Legacy:         true,
	}
	return req.WithContext(tenancy.WithContext(req.Context(), tenant))
}
