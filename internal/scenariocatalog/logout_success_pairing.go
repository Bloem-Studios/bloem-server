package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredLogoutSuccessScenarios contains only the selected unpaired frozen cases.
var RequiredLogoutSuccessScenarios = []string{"logout.ok", "logout.shape"}

func LogoutSuccessAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/logout"}, RequiredLogoutSuccessScenarios)
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
				if s.V2Expectation.OperationID != "logout" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported logout success acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
