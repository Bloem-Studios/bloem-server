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

// AssetKinds are every uploadable asset kind, sorted. Bloem-owned.
func AssetKinds() []AssetKind {
	kinds := make([]AssetKind, 0, len(assetSpecs))
	for kind := range assetSpecs {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// AssetSettingKey is the setting that holds kind's asset reference.
// Bloem-owned.
func AssetSettingKey(kind AssetKind) (string, bool) {
	spec, ok := assetSpecs[kind]
	return spec.settingKey, ok
}
