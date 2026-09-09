package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredSetupStatusScenarios contains only the selected unpaired frozen cases.
var RequiredSetupStatusScenarios = []string{"setup_status.ok", "setup_status.meaning", "setup_status.shape"}

func SetupStatusAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/setup"}, RequiredSetupStatusScenarios)
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
				if s.V2Expectation.OperationID != "getSetupStatus" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported setup status acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
