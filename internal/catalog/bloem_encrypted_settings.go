package catalog

// Bloem keeps user databases as node-local SQLite (internal/userdb), so the
// Silo user-DB S3 credentials are not settings Bloem registers or stores.
// Dropping them here keeps SensitiveSettingKeys in step with Bloem's settings
// contract without editing Silo's map literal.
func init() {
	delete(SensitiveSettingKeys, "s3.user_db_access_key")
	delete(SensitiveSettingKeys, "s3.user_db_secret_key")
}
