package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrFolderDeletionRestricted = errors.New("library is retained by an entitlement bundle")

// CheckDeletionAllowed rejects a known permanent deletion failure before the
// API disables the library. Execution repeats the check under a retained lock.
func (r *FolderRepository) CheckDeletionAllowed(ctx context.Context, id int) error {
	tx, err := r.beginFolderDeletion(ctx, id)
	if err != nil {
		return err
	}
	return tx.Rollback(ctx)
}

// beginFolderDeletion fences new bundle references across the resumable catalog
// batches. The root lock permits ordinary FK checks so an in-flight scanner
// holding a catalog row can finish before a separate batch deletes that row.
func (r *FolderRepository) beginFolderDeletion(ctx context.Context, id int) (pgx.Tx, error) {
	// The guard owns a dedicated connection; fenced bundle writers can occupy
	// catalog-pool slots while waiting. Batches also use their own pool below.
	cfg := r.pool.Config()
	connCfg := cfg.ConnConfig.Copy()
	if cfg.BeforeConnect != nil {
		if err := cfg.BeforeConnect(ctx, connCfg); err != nil {
			return nil, fmt.Errorf("configure folder deletion connection: %w", err)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return nil, fmt.Errorf("connect folder deletion guard: %w", err)
	}
	guard := &folderDeletionGuard{conn: conn}
	if cfg.AfterConnect != nil {
		if err := cfg.AfterConnect(ctx, conn); err != nil {
			guard.close(ctx)
			return nil, fmt.Errorf("configure folder deletion guard: %w", err)
		}
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		guard.close(ctx)
		return nil, fmt.Errorf("begin folder deletion guard: %w", err)
	}
	guard.Tx = tx
	if _, err := tx.Exec(ctx, `SELECT public.bloem_lock_media_folder_bundle_reference($1)`, int64(id)); err != nil {
		_ = guard.Rollback(ctx)
		return nil, fmt.Errorf("fence folder bundle references: %w", err)
	}
	var existingID int
	err = tx.QueryRow(ctx, `SELECT id FROM media_folders WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&existingID)
	if err != nil {
		_ = guard.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, fmt.Errorf("lock folder before delete: %w", err)
	}
	var retained bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entitlement_bundle_members WHERE media_folder_id=$1)`, id).Scan(&retained)
	if err != nil || retained {
		_ = guard.Rollback(ctx)
		if err != nil {
			return nil, fmt.Errorf("check folder deletion references: %w", err)
		}
		return nil, ErrFolderDeletionRestricted
	}
	return guard, nil
}

// folderDeletionWorker reserves one batch connection independently of the
// caller's pool. A writer waiting on the guard must not consume the last slot
// required to finish the deletion that will release it. Connect before any
// destructive phase, and retain the original connection and pool hooks.
func (r *FolderRepository) folderDeletionWorker(ctx context.Context) (*FolderRepository, error) {
	cfg := r.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create folder deletion worker: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect folder deletion worker: %w", err)
	}
	return NewFolderRepository(pool), nil
}

type folderDeletionGuard struct {
	pgx.Tx
	conn *pgx.Conn
}

func (g *folderDeletionGuard) close(ctx context.Context) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = g.conn.Close(closeCtx)
}

func (g *folderDeletionGuard) Commit(ctx context.Context) error {
	err := g.Tx.Commit(ctx)
	g.close(ctx)
	return err
}

func (g *folderDeletionGuard) Rollback(ctx context.Context) error {
	err := g.Tx.Rollback(ctx)
	g.close(ctx)
	return err
}

// finishFolderDeletion moves shared collections off the folder, releases live
// grants, removes the root and recomputes surviving series atomically. Immutable bundle members and
// entitlement audit events are never rewritten.
func finishFolderDeletion(ctx context.Context, tx pgx.Tx, id int) error {
	if err := moveCollectionsOffGuardedFolder(ctx, tx, id); err != nil {
		return fmt.Errorf("deleting folder: %w", err)
	}
	// The folder's episode memberships cascade with it; series that survive in
	// other libraries recompute the denorms read from them, as Silo does.
	var seriesIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(array_agg(DISTINCT e.series_id), ARRAY[]::text[])
		FROM episode_libraries el
		JOIN episodes e ON e.content_id = el.episode_id
		WHERE el.media_folder_id = $1`, id).Scan(&seriesIDs); err != nil {
		return fmt.Errorf("listing folder series: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=$1`, id); err != nil {
		return fmt.Errorf("releasing folder entitlements: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, id); err != nil {
		return fmt.Errorf("deleting folder: %w", err)
	}
	if err := RecomputeSeriesEpisodeMembershipDenorms(ctx, tx, seriesIDs); err != nil {
		return fmt.Errorf("recomputing series membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit folder deletion: %w", err)
	}
	return nil
}

// moveCollectionsOffGuardedFolder runs Silo's moveCollectionsOffFolder under
// the deletion guard. Silo starts a fresh transaction when the collections
// change or a deadlock aborts the attempt; the guard must keep its folder lock,
// so each attempt runs in a savepoint whose rollback drops only its own locks.
func moveCollectionsOffGuardedFolder(ctx context.Context, tx pgx.Tx, id int) error {
	attemptMove := func() error {
		savepoint, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = savepoint.Rollback(ctx) }()
		if err := moveCollectionsOffFolder(ctx, savepoint, id); err != nil {
			return err
		}
		return savepoint.Commit(ctx)
	}
	err := retryOnDeadlock(ctx, attemptMove)
	for attempt := 1; errors.Is(err, errFolderCollectionsChanged) && attempt < 5; attempt++ {
		err = retryOnDeadlock(ctx, attemptMove)
	}
	return err
}
