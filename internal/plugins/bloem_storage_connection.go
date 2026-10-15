package plugins

import "context"

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
