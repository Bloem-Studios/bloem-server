package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredSessionDeleteRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredSessionDeleteRefusalsScenarios = []string{"session_delete.other_user", "session_delete.unknown", "session_delete.no_token"}

func SessionDeleteRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodDelete, []string{"/api/v1/auth/sessions/{id}"}, RequiredSessionDeleteRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "deleteSession" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported session delete refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
