package plugins

import "github.com/jackc/pgx/v5/pgxpool"

// NativeStorageUsesPool checks the actual registry/configuration stores. It does
// not inspect approvals, execute providers or certify a storage backend.
func (r *NativeStorageRegistry) NativeStorageUsesPool(pool *pgxpool.Pool) bool {
	return r != nil && pool != nil && r.pool == pool && r.configs != nil && r.configs.pool == pool && r.configs.cipher != nil && r.baseDir != ""
}
