package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredLoginInputScenarios contains only the selected unpaired frozen cases.
var RequiredLoginInputScenarios = []string{"login.unknown_provider", "login.missing_fields", "login.malformed_json"}

func LoginInputAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/login"}, RequiredLoginInputScenarios)
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
					return nil, fmt.Errorf("%s: unsupported login input acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
