package config

import "testing"

func TestNormalizeAdminSettingAcceptsFrequencyMatching(t *testing.T) {
	got, err := NormalizeAdminSetting("catalog.search.meilisearch.matching_strategy", "frequency")
	if err != nil || got != "frequency" {
		t.Fatalf("NormalizeAdminSetting(matching_strategy, frequency) = %q, %v; want frequency", got, err)
	}
}
