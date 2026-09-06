package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredSignupStatusScenarios contains only the selected unpaired frozen cases.
var RequiredSignupStatusScenarios = []string{"signup_status.ok", "signup_status.enabled", "signup_status.disabled", "signup_status.shape"}

func SignupStatusAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/signup"}, RequiredSignupStatusScenarios)
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
				if s.V2Expectation.OperationID != "getSignupStatus" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported signup status acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
