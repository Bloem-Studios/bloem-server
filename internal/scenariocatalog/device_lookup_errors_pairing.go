package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredDeviceLookupErrorsScenarios contains only the selected unpaired frozen cases.
var RequiredDeviceLookupErrorsScenarios = []string{"device_lookup.expired", "device_lookup.not_found", "device_lookup.no_params"}

func DeviceLookupErrorsAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/device"}, RequiredDeviceLookupErrorsScenarios)
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
				if s.V2Expectation.OperationID != "getDeviceLogin" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 {
					return nil, fmt.Errorf("%s: unsupported device lookup errors acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
