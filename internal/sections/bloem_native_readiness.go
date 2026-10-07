package sections

import "github.com/jackc/pgx/v5/pgxpool"

// NativeStorageUsesPool reports actual constructor wiring, not actor authority.
func (s *Repository) NativeStorageUsesPool(pool *pgxpool.Pool) bool {
	return s != nil && pool != nil && s.pool == pool
}
