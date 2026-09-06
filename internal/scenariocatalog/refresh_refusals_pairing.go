package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredRefreshRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredRefreshRefusalsScenarios = []string{"refresh.access_token_rejected", "refresh.revoked", "refresh.garbage", "refresh.missing"}

func RefreshRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/refresh"}, RequiredRefreshRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "refreshSession" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported refresh refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
