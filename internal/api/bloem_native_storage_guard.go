package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/plugins"
)

func nativeStorageRegistry(deps Dependencies) plugins.NativeStorageIsolationRegistry {
	if deps.NativeStorage != nil {
		return deps.NativeStorage.Registry
	}
	if deps.DB != nil {
		return nativeStorageMarkers{pool: deps.DB}
	}
	return nil
}

// Embedded routers without a native runtime still consult durable markers;
// ordinary plugin administration does not require enabling native readers.
type nativeStorageMarkers struct{ pool *pgxpool.Pool }

func (s nativeStorageMarkers) NativeStorageIDs(ctx context.Context, ids []int) (map[int]bool, error) {
	result := make(map[int]bool)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT installation_id FROM bloem_storage_installations WHERE installation_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
