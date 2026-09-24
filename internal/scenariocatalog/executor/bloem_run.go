package executor

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// bloemCurrentCatalogs narrows RunAll's catalogs to the rows Bloem still
// executes (scenariocatalog.BloemCurrentCatalogs).
func bloemCurrentCatalogs(t *testing.T, catalogs []*scenariocatalog.Catalog) []*scenariocatalog.Catalog {
	t.Helper()
	current, err := scenariocatalog.BloemCurrentCatalogs(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

// withBloemRowFixtures installs the row-scoped Bloem fixtures in order and
// returns their teardown, which runs them in reverse like stacked defers
// (each teardown still runs if a later one panics or calls t.FailNow).
// Retained Bloem extensions need credentials/playback fixtures that must not
// change the frozen Silo rows' starting state.
func (e *Env) withBloemRowFixtures(row scenariocatalog.Row) func() {
	setups := []func(scenariocatalog.Row) func(){
		e.withBloemRowFixture,
		e.withLocalAvatarRowFixture,
		e.withBloemDeviceRowFixture,
		e.withBloemSectionRowFixture,
	}
	var teardowns []func()
	var runFrom func(i int)
	runFrom = func(i int) {
		if i < 0 {
			return
		}
		defer runFrom(i - 1)
		teardowns[i]()
	}
	installed := false
	defer func() {
		// A setup that unwinds (t.Fatal) still tears down the earlier ones.
		if !installed {
			runFrom(len(teardowns) - 1)
		}
	}()
	for _, setup := range setups {
		teardowns = append(teardowns, setup(row))
	}
	installed = true
	return func() { runFrom(len(teardowns) - 1) }
}

// bloemLifecycleStoreGate reports whether the transport needs the lifecycle
// store. Missing lifecycle readiness is reported as a failure rather than a
// skip: lifecycle requests must not test validation against a dead store.
// The gate's offline_candidates count is only a static candidate set; router
// registration and lifecycle readiness are execution prerequisites.
func (e *Env) bloemLifecycleStoreGate(t *testing.T, res *Result, s scenariocatalog.Scenario, transport, method string) bool {
	t.Helper()
	if !lifecycleStoreRequired(s, transport, method) {
		return false
	}
	if !e.HasDatabase() {
		res.Failures = []string{"execution prerequisite: requires a reachable lifecycle store; set " + DatabaseEnv + " to an owned scratch database"}
		t.Fatal(res.Failures[0])
	}
	return true
}

// bloemScenarioNeedsLifecycleStore reports whether either transport of s
// needs the lifecycle store, which makes the row database-gated.
func bloemScenarioNeedsLifecycleStore(row scenariocatalog.Row, s scenariocatalog.Scenario) bool {
	if lifecycleStoreRequired(s, "v1", row.Method) {
		return true
	}
	return s.V2Expectation != nil && lifecycleStoreRequired(v2Scenario(s), "v2", s.V2Expectation.Method)
}
