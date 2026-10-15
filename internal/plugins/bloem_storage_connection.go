package plugins

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

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
		return 0, storagesource.ErrSourceUnavailable
	}
	current, err := r.ConnectionConfig(ctx, installationID)
	if err != nil {
		return 0, err
	}
	next, err := overlayS3Credentials(current, c)
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
