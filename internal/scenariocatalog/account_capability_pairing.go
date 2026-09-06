package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredAccountCapabilityScenarios contains only the selected unpaired frozen cases.
var RequiredAccountCapabilityScenarios = []string{
	"account_capability.ok", "account_capability.meaning", "account_capability.no_profile", "account_capability.secondary_profile", "account_capability.no_token", "account_capability.bad_token", "account_capability.error_shape",
}

func AccountCapabilityAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/account/capability"}, RequiredAccountCapabilityScenarios)
	if err != nil {
		return nil, err
	}
	for _, c := range selected {
		for _, r := range c.Rows {
			for _, s := range r.Scenarios {
				for _, requirement := range s.Requires {
					if requirement != "database" {
						return nil, fmt.Errorf("%s: unsupported requirement", s.ID)
					}
				}
				if s.V2Expectation.OperationID != "getAccountPasswordCapability" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported account capability acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
