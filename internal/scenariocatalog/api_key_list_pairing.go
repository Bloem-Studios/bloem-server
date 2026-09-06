package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredAPIKeyListScenarios contains only the selected unpaired frozen cases.
var RequiredAPIKeyListScenarios = []string{
	"keys_list.ok",
	"keys_list.sorted", "keys_list.empty", "keys_list.no_token",
}

func APIKeyListAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/api-keys/"}, RequiredAPIKeyListScenarios)
	if err != nil {
		return nil, err
	}
	for _, c := range selected {
		for _, r := range c.Rows {
			for _, s := range r.Scenarios {
				if s.V2Expectation.OperationID != "listPersonalAPIKeys" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 || len(s.Requires) != 0 {
					return nil, fmt.Errorf("%s: unsupported list acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
