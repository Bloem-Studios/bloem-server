package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredDeviceStartRefusalsScenarios contains only the selected unpaired frozen cases.
var RequiredDeviceStartRefusalsScenarios = []string{"device_start.bad_purpose", "device_start.malformed"}

func DeviceStartRefusalsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodPost, []string{"/api/v1/auth/device/start"}, RequiredDeviceStartRefusalsScenarios)
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
				if s.V2Expectation.OperationID != "startDeviceLogin" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported device start refusals acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
