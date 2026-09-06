package scenariocatalog

import (
	"fmt"
	"net/http"
)

const meImpersonationLegacyRoute = "/api/v1/auth/me"

// RequiredMeImpersonationScenarios contains only the selected unpaired frozen cases.
var RequiredMeImpersonationScenarios = []string{"me.impersonation"}

func MeImpersonationAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{meImpersonationLegacyRoute}, RequiredMeImpersonationScenarios)
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
				if s.V2Expectation.OperationID != "getCurrentUser" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported impersonated account read acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
