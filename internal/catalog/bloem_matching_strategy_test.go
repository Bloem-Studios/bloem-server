package catalog

import "testing"

func TestCatalogSearchSettingsAcceptFrequency(t *testing.T) {
	settings, err := CatalogSearchSettingsFromMap(map[string]string{
		SearchSettingMeilisearchMatchingStrategy: "frequency",
	})
	if err != nil {
		t.Fatalf("CatalogSearchSettingsFromMap: %v", err)
	}
	if settings.MatchingStrategy != "frequency" {
		t.Fatalf("MatchingStrategy = %q, want frequency", settings.MatchingStrategy)
	}
}

// Two-word searches stay strict under frequency for the reason they do under
// last: dropping a word after typo correction leaves one loose word.
func TestFrequencyKeepsTwoWordSearchesStrict(t *testing.T) {
	p := &MeilisearchSearchProvider{config: MeilisearchProviderConfig{MatchingStrategy: "frequency"}}
	for query, want := range map[string]string{
		"heist":                 "frequency",
		"spnge bob":             "all",
		"boy haunted by ghost":  "frequency",
		"orphaned dinosaur and": "all",
	} {
		if got := p.matchingStrategyForRequest(CatalogSearchRequest{Query: query}); got != want {
			t.Errorf("matchingStrategyForRequest(%q) = %q, want %q", query, got, want)
		}
	}
}
