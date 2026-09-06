package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredSignupRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredSignupRefusalsScenarios = []string{"signup.disabled_setting", "signup.missing_fields"}

func SignupRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/signup"}, RequiredSignupRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "signup" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported signup refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
