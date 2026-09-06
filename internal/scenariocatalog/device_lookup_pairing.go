package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredDeviceLookupScenarios contains only the selected unpaired frozen cases.
var RequiredDeviceLookupScenarios = []string{"device_lookup.by_token", "device_lookup.by_code", "device_lookup.meaning", "device_lookup.shape"}

func DeviceLookupAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/auth/device"}, RequiredDeviceLookupScenarios)
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
					return nil, fmt.Errorf("%s: unsupported device lookup acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
