package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredAuthProvidersScenarios contains only the selected unpaired frozen cases.
var RequiredAuthProvidersScenarios = []string{"providers.ok", "providers.local_default", "providers.shape", "providers.sorted", "providers.oauth_hidden"}

const frozenDatabaseRequirement = "database"

func AuthProvidersAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/providers"}, RequiredAuthProvidersScenarios)
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
				if s.V2Expectation.OperationID != "listAuthProviders" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported auth providers acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
