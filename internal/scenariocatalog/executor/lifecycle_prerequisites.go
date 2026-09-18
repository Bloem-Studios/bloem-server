package executor

import (
	"net/url"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// lifecycleStoreRequired uses the same route registry as production preflight.
// Even malformed public input needs a readable phase before validation. Keep
// declared outage scenarios on the dead pool, and never change their oracle.
// s is the transport-specific scenario, after v2Scenario when applicable.
func lifecycleStoreRequired(s scenariocatalog.Scenario, transport, method string) bool {
	if s.HasRequirement("database_unavailable") {
		return false
	}
	matched := func(method, path string) bool {
		u, err := url.Parse(path)
		if err != nil {
			return false // buildRequest reports malformed URLs separately.
		}
		_, ok := api.MatchLifecycleRoute(method, u.Path)
		return ok
	}
	if matched(method, s.Request.Path) {
		return true
	}
	for _, step := range s.Then {
		if matched(step.Method, step.Request.Path) {
			return true
		}
	}
	if transport == "v2" && s.V2Expectation != nil {
		for _, step := range s.V2Expectation.Then {
			if matched(step.Method, step.Request.Path) {
				return true
			}
		}
	}
	return false
}
