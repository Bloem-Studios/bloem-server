package handlers

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"encoding/json"
	"testing"
)

// TestSectionSettingsResponsePinsWire pins the GET /profile/sections/settings
// body: the named sectionSettingsResponse replaced an inline
// map[string][]settingsEntry literal, and this test proves the marshalled
// bytes did not change — same field names, same tags, same omitempty, same
// order of population.
func TestSectionSettingsResponsePinsWire(t *testing.T) {
	resp := sectionSettingsResponse{Sections: []sectionSettingsEntry{
		{
			ID:          "continue",
			SectionType: "continue_watching",
			Title:       "Continue Watching",
			Featured:    true,
			ItemLimit:   20,
			Hidden:      false,
			IsCustom:    false,
			Customized:  true,
			Position:    3,
			Config:      json.RawMessage(`{"continue_type":"all"}`),
		},
		{
			ID:          "next_up",
			SectionType: "next_up",
			Title:       "Next Up",
			ItemLimit:   12,
			Position:    4,
		},
	}}
	want := `{"sections":[` +
		`{"id":"continue","section_type":"continue_watching","title":"Continue Watching","featured":true,"item_limit":20,"hidden":false,"is_custom":false,"customized":true,"position":3,"config":{"continue_type":"all"}},` +
		`{"id":"next_up","section_type":"next_up","title":"Next Up","featured":false,"item_limit":12,"hidden":false,"is_custom":false,"customized":false,"position":4}` +
		`]}`
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}
