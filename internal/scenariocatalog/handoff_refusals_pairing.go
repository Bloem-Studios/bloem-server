package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredHandoffRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredHandoffRefusalsScenarios = []string{"handoff.no_profile", "handoff.locked_unverified", "handoff.purpose_mismatch", "handoff.other_account_profile", "handoff.no_token"}

func HandoffRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/device/approve-handoff"}, RequiredHandoffRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "approveDeviceHandoff" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported handoff refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
