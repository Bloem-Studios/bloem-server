package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

var accessGroupHandlerOrganizationID = uuid.MustParse("10000000-0000-0000-0000-000000000001")

func TestAccessGroupHandlerUsesOnlyValidatedTenantContext(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	handler := NewAccessGroupHandler(store)
	foreignOrganizationID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/access-groups", strings.NewReader(`{
		"name": "Tenant Bound",
		"organization_id": "20000000-0000-0000-0000-000000000002"
	}`))
	req.Header.Set("X-Organization-ID", foreignOrganizationID.String())
	req = accessGroupRequestWithTenant(req)
	rec := httptest.NewRecorder()

	handler.HandleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HandleCreate status = %d, body %s", rec.Code, rec.Body.String())
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
