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
