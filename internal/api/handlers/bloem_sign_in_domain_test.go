package handlers

import (
	"errors"
	"fmt"
	"net/http"
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
