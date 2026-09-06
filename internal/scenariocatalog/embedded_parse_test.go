package scenariocatalog

import "testing"

// The embedded v2 document and frozen originals parse once per process; every
// validator reads that single result. A parse failure must surface directly.
func TestEmbeddedDocumentsParseOnce(t *testing.T) {
	paths, err := openAPIPaths()
	if err != nil || len(paths) == 0 {
		t.Fatalf("embedded OpenAPI: %v", err)
	}
	again, err := openAPIPaths()
	if err != nil || len(again) != len(paths) {
		t.Fatal("embedded OpenAPI must parse once and be reused")
	}
	if op, ok := paths["/api/v2/devices"]["get"]; !ok || op.OperationID != deviceListOperation {
		t.Fatal("embedded OpenAPI lost the device list operation")
	}

	originals, err := frozenSequenceOriginals()
	if err != nil || len(originals) != 2 {
		t.Fatalf("frozen originals: %v (%d)", err, len(originals))
	}
	repeats := map[string]int{}
	for _, o := range originals {
		repeats[o.Scenario.ID] = o.Scenario.Request.Repeat
	}
	if repeats["device_lookup.rate_limited.r1"] != 21 || repeats["device_poll.rate_limited.r1"] != 31 {
		t.Fatalf("frozen originals changed: %v", repeats)
	}
	second, err := frozenSequenceOriginals()
	if err != nil || &second[0] != &originals[0] {
		t.Fatal("frozen originals must parse once and be reused")
	}
}
