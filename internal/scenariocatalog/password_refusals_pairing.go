package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredPasswordRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredPasswordRefusalsScenarios = []string{"password.wrong_current", "password.weak", "password.too_long", "password.missing_fields", "password.malformed_json"}

func PasswordRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/account/password"}, RequiredPasswordRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "changePassword" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported password refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
