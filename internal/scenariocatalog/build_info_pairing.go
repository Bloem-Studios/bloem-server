package scenariocatalog

import (
	"fmt"
	"net/http"
)

// RequiredBuildInfoScenarios contains only the selected unpaired frozen cases.
var RequiredBuildInfoScenarios = []string{
	"build.ok", "build.meaning", "build.shape", "build.no_token",
}

func BuildInfoAcceptance(catalogs []*Catalog) ([]*Catalog, error) {
	selected, err := requiredAcceptance(catalogs, http.MethodGet, []string{"/api/v1/admin/system/build"}, RequiredBuildInfoScenarios)
	if err != nil {
		return nil, err
	}
	for _, c := range selected {
		for _, r := range c.Rows {
			for _, s := range r.Scenarios {
				if s.V2Expectation.OperationID != "getAdminBuildInfo" || len(s.Then) != 0 || len(s.V2Expectation.Then) != 0 || len(s.Requires) != 0 {
					return nil, fmt.Errorf("%s: unsupported build metadata acceptance sequence", s.ID)
				}
			}
		}
	}
	return selected, nil
}
