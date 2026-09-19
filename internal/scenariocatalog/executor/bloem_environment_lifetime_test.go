package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

func TestBloemScenarioEnvironmentLifetime(t *testing.T) {
	t.Setenv(DatabaseEnv, "")
	var owned context.Context
	t.Run("owning test", func(t *testing.T) {
		e := New(t)
		owned = e.appCtx
		if owned.Err() != nil {
			t.Fatal("environment started canceled")
		}
		// Registered last, this runs before server and pool teardown. Router
		// maintenance must receive cancellation before those resources close.
		t.Cleanup(func() {
			if e.ctx.Err() != nil {
				t.Error("fixture cleanup lost its SQL context")
			}
			if !errors.Is(owned.Err(), context.Canceled) {
				t.Error("environment context remained live during resource cleanup")
			}
		})
	})
	if owned == nil || !errors.Is(owned.Err(), context.Canceled) {
		t.Error("router application context outlived its owning test")
	}
}

func TestBloemScenarioReseedBoundaries(t *testing.T) {
	catalogs, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := New(t)
	if !e.HasDatabase() {
		t.Skip(DatabaseEnv + " is not set")
	}
	for _, tc := range []struct {
		id     string
		before []int
	}{
		// The unmodified frozen mutation asserts both transports grow 5 to
		// 7. One reset before and one after each transport preserve isolation.
		{"codes_topup.meaning", []int{5, 7, 5, 7}},
		// Paired reads still reset before each transport, even without the
		// mutation scenario's explicit FreshState flag.
		{"codes_list.ok", []int{5, 5}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var catalog *scenariocatalog.Catalog
			var selectedRow scenariocatalog.Row
			var selected scenariocatalog.Scenario
			for _, c := range catalogs {
				for _, row := range c.Rows {
					for _, scenario := range row.Scenarios {
						if scenario.ID == tc.id {
							catalog, selectedRow, selected = c, row, scenario
						}
					}
				}
			}
			if catalog == nil || selected.V2Expectation == nil {
				t.Fatal("required frozen paired scenario is missing")
			}
			var before []int
			e.beforeReseed = func() {
				var uses int
				if err := e.pool.QueryRow(e.ctx, `SELECT max_uses FROM invite_codes WHERE code=$1`, inviteCode).Scan(&uses); err != nil {
					t.Fatal(err)
				}
				before = append(before, uses)
			}
			defer func() { e.beforeReseed = nil }()
			var results []Result
			e.Run(t, catalog, selectedRow, selected, func(r Result) { results = append(results, r) })
			if len(results) != 2 {
				t.Fatalf("executed %d transports, want both frozen transports", len(results))
			}
			for _, result := range results {
				if !result.Passed() {
					t.Errorf("%s did not pass its unchanged assertions", result.Transport)
				}
			}
			if !reflect.DeepEqual(before, tc.before) {
				t.Errorf("state before reseeds = %v, want %v", before, tc.before)
			}
			var uses int
			if err := e.pool.QueryRow(e.ctx, `SELECT max_uses FROM invite_codes WHERE code=$1`, inviteCode).Scan(&uses); err != nil {
				t.Fatal(err)
			}
			if uses != 5 {
				t.Fatalf("transport left mutated fixture: max_uses=%d", uses)
			}
		})
	}
}
