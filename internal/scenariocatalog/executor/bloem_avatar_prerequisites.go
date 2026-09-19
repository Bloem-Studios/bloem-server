package executor

import "github.com/Silo-Server/silo-server/internal/scenariocatalog"

// These packets exercise multipart validation and profile ownership. Supply a
// real local store so they reach those checks. The two historical missing-store
// packets keep the unconfigured router and their incompatible panic assertions;
// selecting a fixture must never depend on the expected response status.
func bloemAvatarStorageRequired(row scenariocatalog.Row, s scenariocatalog.Scenario) bool {
	if row.Method != "PUT" || row.Path != "/api/v1/profiles/{id}/avatar" || s.HasRequirement("database_unavailable") {
		return false
	}
	switch s.ID {
	case "avatar_upload.raw", "avatar_upload.missing_file", "avatar_upload.not_multipart",
		"avatar_upload.not_found", "avatar_upload.shape", "avatar_upload.any_profile",
		"avatar_upload.other_account_profile", "avatar_upload.other_account_path", "avatar_upload.no_token":
		return true
	default:
		return false
	}
}

func (e *Env) withBloemAvatarScenarioFixture(row scenariocatalog.Row, s scenariocatalog.Scenario) func() {
	if !e.HasDatabase() || !bloemAvatarStorageRequired(row, s) {
		return func() {}
	}
	return e.withLocalAvatarFixture()
}
