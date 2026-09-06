package scenariocatalog

import (
	"fmt"
	"net/http"
)

const loginCredentialsLegacyRoute = "/api/v1/auth/login"

// RequiredLoginCredentialsScenarios contains only the selected unpaired frozen cases.
var RequiredLoginCredentialsScenarios = []string{"login.wrong_password", "login.unknown_user", "login.disabled"}

func LoginCredentialsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{loginCredentialsLegacyRoute}, RequiredLoginCredentialsScenarios)
	if err != nil {
		return nil, err
	}
	for _, c := range selected {
		for _, r := range c.Rows {
			for _, s := range r.Scenarios {
				for _, requirement := range s.Requires {
					if requirement != frozenDatabaseRequirement {
						return nil, fmt.Errorf("%s: unsupported requirement", s.ID)
					}
				}
				if s.V2Expectation.OperationID != "login" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported login credentials acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
