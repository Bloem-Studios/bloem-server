package handlers

import (
	"encoding/json"
	"testing"
)

func TestEffectiveSettingsEnvelopePreservesResponseWire(t *testing.T) {
	payload, err := json.Marshal(effectiveSettingValuesResponse{
		Settings: []effectiveSettingValueResponse{{Key: "playback.preferred_quality", Value: json.RawMessage(`"720p"`), Source: "profile", Constrained: true, ConstraintKind: "ceiling", StoredValue: json.RawMessage(`"1080p"`), DefinitionRevision: 1}},
		Revision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["revision"]) != "7" {
		t.Fatalf("revision lost: %s", payload)
	}
	var rows []effectiveSettingValueResponse
	if err := json.Unmarshal(decoded["settings"], &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || string(rows[0].Value) != `"720p"` || string(rows[0].StoredValue) != `"1080p"` || !rows[0].Constrained {
		t.Fatalf("effective/authored values changed: %s", payload)
	}
}
