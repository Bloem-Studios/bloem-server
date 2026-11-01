package apiv2

import (
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// A sign-in on another organization's domain is a conflict whose detail names
// the account's own address: the sign-in page shows that detail. A 403 would
// render as permission_denied, which the page reports as a disabled account.
func TestLoginProblemForOrganizationDomainIsConflict(t *testing.T) {
	p := loginProblem(&handlers.APIError{Status: http.StatusConflict, Code: "wrong_organization_domain", Message: "This account signs in at https://main.domain.com."})
	if p.Type != TypeConflict.URI() || p.Detail != "This account signs in at https://main.domain.com." {
		t.Fatalf("problem = %+v", p)
	}
}
