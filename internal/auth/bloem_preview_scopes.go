package auth

const ScopeAdminPreview = "admin:preview"

func bloemPreviewAPIKeyScopes() []APIKeyScope {
	return []APIKeyScope{{Name: ScopeAdminPreview, Description: "Configure the read-only preview, its launch keys and curated catalog. Requires a Platform administrator."}}
}
