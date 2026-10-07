package resourcetenancy

import "github.com/jackc/pgx/v5/pgxpool"

// NativeStorageUsesPool reports actual constructor wiring, not actor authority.
func (s *Store) NativeStorageUsesPool(pool *pgxpool.Pool) bool {
	return s != nil && pool != nil && s.pool == pool
}
