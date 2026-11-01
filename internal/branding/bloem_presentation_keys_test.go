package branding

import (
	"slices"
	"testing"
)

func TestPresentationSettingKeysCoverEveryAsset(t *testing.T) {
	keys := PresentationSettingKeys()
	if !slices.IsSorted(keys) {
		t.Fatalf("keys are not sorted: %v", keys)
	}
	for _, key := range []string{KeyServerName, KeyLoginSubtitle, KeyAccentColor, KeyDefaultTheme} {
		if !slices.Contains(keys, key) {
			t.Errorf("missing %s", key)
		}
	}
	for kind, spec := range assetSpecs {
		if !slices.Contains(keys, spec.settingKey) {
			t.Errorf("asset kind %s: missing %s", kind, spec.settingKey)
		}
	}
	if len(keys) != 4+len(assetSpecs) {
		t.Fatalf("keys = %v, want the four values and one per asset kind", keys)
	}
}
