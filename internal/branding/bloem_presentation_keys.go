package branding

import "slices"

// PresentationSettingKeys are the settings a Snapshot is assembled from: the
// server name, login subtitle, accent colour, default theme and every asset
// reference, sorted. internal/orgdomains answers them per organization.
// Bloem-owned.
func PresentationSettingKeys() []string {
	keys := []string{KeyServerName, KeyLoginSubtitle, KeyAccentColor, KeyDefaultTheme}
	for _, spec := range assetSpecs {
		keys = append(keys, spec.settingKey)
	}
	slices.Sort(keys)
	return keys
}
