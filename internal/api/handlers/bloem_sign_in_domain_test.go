package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestOrganizationDomainAPIError(t *testing.T) {
	err := fmt.Errorf("create session: %w", &auth.OrganizationDomainError{SignInURL: "https://main.domain.com"})
	apiErr := organizationDomainAPIError(err)
	if apiErr == nil || apiErr.Status != http.StatusConflict || apiErr.Code != "wrong_organization_domain" ||
		apiErr.Message != "This account signs in at https://main.domain.com." {
		t.Fatalf("APIError = %+v", apiErr)
	}
	if got := organizationDomainAPIError(&auth.OrganizationDomainError{}); got == nil || got.Message != "This account signs in at its organization's own address." {
		t.Fatalf("without an address = %+v", got)
	}
	if organizationDomainAPIError(errors.New("other")) != nil {
		t.Fatal("an unrelated error mapped to the refusal")
	}
}

func TestProfileLoginOnAnotherOrganizationsDomain(t *testing.T) {
	handler := &AuthHandler{profileLogin: &profileLoginStub{err: &auth.OrganizationDomainError{SignInURL: "https://main.domain.com"}}}
	req := httptest.NewRequest(http.MethodPost, "/auth/profile-login", strings.NewReader(`{"email":"reader@example.test","password":"profile-password","device_id":"tablet-17"}`))
	rec := httptest.NewRecorder()
	handler.HandleProfileLogin(rec, req)
	var body errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || rec.Code != http.StatusConflict ||
		body.Error != "wrong_organization_domain" || body.Message != "This account signs in at https://main.domain.com." {
		t.Fatalf("status %d body %+v %v", rec.Code, body, err)
	}
}
