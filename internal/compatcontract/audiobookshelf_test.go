package compatcontract

import "testing"

func TestBaselineFixturesUseReservedValues(t *testing.T) {
	for _, suite := range []Suite{JellyfinBaseline(), AudiobookshelfBaseline()} {
		if suite.Name == "" || len(suite.Cases) == 0 {
			t.Fatalf("suite = %#v, want a named suite with cases", suite)
		}
		for _, c := range suite.Cases {
			if c.Name == "" || c.Path == "" {
				t.Fatalf("%s has incomplete case %#v", suite.Name, c)
			}
		}
	}
}
