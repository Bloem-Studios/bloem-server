package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredDeviceDenyRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredDeviceDenyRefusalsScenarios = []string{"deny.expired", "deny.not_found", "deny.no_token"}

func DeviceDenyRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/device/deny"}, RequiredDeviceDenyRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "denyDeviceLogin" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported device deny refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
