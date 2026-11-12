package plugins

import (
	"context"
	"errors"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pickConnectionConfig is the lenient reader used for summaries: it falls back
// to the first config when no `connection` key exists. Never use it to decide
// what a write overlays; see pickConnectionConfigStrict.
func pickConnectionConfig(configs []*RuntimeConfig) map[string]any {
	for _, c := range configs {
		if c != nil && c.Key == "connection" {
			return c.Value
		}
	}
	if len(configs) > 0 && configs[0] != nil {
		return configs[0].Value
	}
	return nil
}

// pickConnectionConfigStrict returns only the explicit `connection` runtime
// config, with no fallback. Credential rotation overlays this value, so it must
// never silently adopt some other config.
func pickConnectionConfigStrict(configs []*RuntimeConfig) map[string]any {
	for _, c := range configs {
		if c != nil && c.Key == "connection" {
			return c.Value
		}
	}
	return nil
}

// rotationLookupError maps only "no matching source row" to source
// unavailable; any other database error stays a server error.
func rotationLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return storagesource.ErrSourceUnavailable
	}
	return err
}

// ConnectionConfig returns the decrypted `connection` runtime config of an
// installation for host-side use only. Callers must never serialize it.
func (r *NativeStorageRegistry) ConnectionConfig(ctx context.Context, installationID int) (map[string]any, error) {
	if r == nil || r.configs == nil {
		return nil, nil
	}
	configs, err := r.configs.ListGlobalConfigs(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return pickConnectionConfig(configs), nil
}

// S3Credentials are the only fields a rotation may change.
type S3Credentials struct{ AccessKey, SecretKey, SessionToken string }

// overlayS3Credentials copies the stored connection and replaces only the three
// credential fields, so every other setting is unchanged by construction.
func overlayS3Credentials(current map[string]any, c S3Credentials) (map[string]map[string]any, error) {
	if current == nil || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("credential rotation requires a stored connection and both keys")
	}
	conn := make(map[string]any, len(current)+3)
	for k, v := range current {
		conn[k] = v
	}
	delete(conn, "session_token")
	conn["access_key"], conn["secret_key"] = c.AccessKey, c.SecretKey
	if c.SessionToken != "" {
		conn["session_token"] = c.SessionToken
	}
	return map[string]map[string]any{"connection": conn}, nil
}

// s3CarriedSettingKeys are the optional S3 settings an edit keeps when it does
// not send them: the request limits and the proxy address. The proxy user and
// password follow the rules in carryS3StoredSettings.
var s3CarriedSettingKeys = []string{"auth_failure_budget", "auth_failure_window_seconds", "auth_pause_seconds", "auth_pause_max_seconds",
	"max_concurrent_requests", "max_listing_requests", "max_read_mbps", "http_proxy"}

// carryS3StoredSettings returns config with the stored values of the optional
// S3 settings the submitted connection leaves out, so an edit form that does
// not know them, or may not show them, does not reset them. A field sent in
// any spelling encoding/json matches counts as sent, and a field sent as null
// is cleared. The proxy user is kept only while a proxy remains, and the
// write-only proxy password only for the same proxy and user, so a stored
// password never goes to a proxy it was not entered for. Credentials and every
// other field are never carried. Neither input is modified.
func carryS3StoredSettings(stored map[string]any, config map[string]map[string]any) map[string]map[string]any {
	submitted, ok := config["connection"]
	if !ok || submitted == nil {
		return config
	}
	conn := make(map[string]any, len(submitted)+len(s3CarriedSettingKeys)+2)
	for k, v := range submitted {
		conn[k] = v
	}
	// carry reports whether key was sent; a null exact key is a clear.
	carry := func(key string, allowed bool) {
		for k := range submitted {
			if strings.EqualFold(k, key) {
				if k == key && submitted[k] == nil {
					delete(conn, key)
				}
				return
			}
		}
		if v, ok := stored[key]; ok && allowed {
			conn[key] = v
		}
	}
	text := func(m map[string]any, key string) string { v, _ := m[key].(string); return v }
	for _, key := range s3CarriedSettingKeys {
		carry(key, true)
	}
	proxy := text(conn, "http_proxy")
	carry("http_proxy_username", proxy != "")
	carry("http_proxy_password", proxy != "" && proxy == text(stored, "http_proxy") && text(conn, "http_proxy_username") == text(stored, "http_proxy_username"))
	next := make(map[string]map[string]any, len(config))
	for k, v := range config {
		next[k] = v
	}
	next["connection"] = conn
	return next
}

// withStoredS3Settings applies carryS3StoredSettings to an edit of an attached
// S3 source; any other source's configuration is returned unchanged. As with
// credential rotation, the caller's expected revision fences the read: a write
// after it increments the revision, so the replacement fails rather than
// committing settings carried from a stale read.
func (r *NativeStorageRegistry) withStoredS3Settings(ctx context.Context, key, owner uuid.UUID, config map[string]map[string]any) (map[string]map[string]any, error) {
	var installationID int
	err := r.pool.QueryRow(ctx, `SELECT installation_id FROM bloem_storage_sources WHERE key=$1 AND owner_id=$2 AND plugin_id=$3 AND installation_id IS NOT NULL`, key, owner, nativeS3PluginID).Scan(&installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return config, nil
	}
	if err != nil {
		return nil, err
	}
	if r.configs == nil {
		return nil, nativeManagementUnavailable()
	}
	configs, err := r.configs.ListGlobalConfigs(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return carryS3StoredSettings(pickConnectionConfigStrict(configs), config), nil
}

// rotateCredentials replaces the stored S3 credentials of an in-use source. It
// reads the stored connection, overlays only the credential fields and applies
// it through the normal replacement transaction with the empty-namespace rule
// waived. The caller's expected revision fences concurrent edits: every write
// that changes the stored config or the source's installation increments the
// source revision, so a read that raced such a write fails the in-transaction
// revision check instead of committing a stale overlay.
func (r *NativeStorageRegistry) rotateCredentials(ctx context.Context, key, owner uuid.UUID, c S3Credentials, expected int64, authorize NativeStorageAuthorizeTx) (int64, error) {
	var installationID int
	if err := r.pool.QueryRow(ctx, `SELECT installation_id FROM bloem_storage_sources WHERE key=$1 AND owner_id=$2 AND plugin_id=$3 AND installation_id IS NOT NULL`, key, owner, nativeS3PluginID).Scan(&installationID); err != nil {
		return 0, rotationLookupError(err)
	}
	if r.configs == nil {
		return 0, nativeManagementUnavailable()
	}
	configs, err := r.configs.ListGlobalConfigs(ctx, installationID)
	if err != nil {
		return 0, err
	}
	next, err := overlayS3Credentials(pickConnectionConfigStrict(configs), c)
	if err != nil {
		return 0, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return r.replaceConfigurationMode(ctx, key, owner, next, &expected, authorize, true)
}

// RotateCredentials is the authorized entry point for credential rotation. It
// refuses to run without retained authority: the empty-namespace waiver and
// the revision fence both live in the authorized transaction path.
func (r *NativeStorageRegistry) RotateCredentials(ctx context.Context, key, owner uuid.UUID, c S3Credentials, expected int64, authorize NativeStorageAuthorizeTx) (int64, error) {
	if r == nil || r.pool == nil || authorize == nil {
		return 0, nativeManagementUnavailable()
	}
	if key == uuid.Nil || owner == uuid.Nil || expected <= 0 {
		return 0, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return r.rotateCredentials(ctx, key, owner, c, expected, authorize)
}
