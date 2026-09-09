package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredDeviceApproveRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredDeviceApproveRefusalsScenarios = []string{"approve.expired", "approve.purpose_mismatch", "approve.not_found", "approve.no_token"}

func DeviceApproveRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/device/approve"}, RequiredDeviceApproveRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "approveDeviceLogin" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported device approve refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
