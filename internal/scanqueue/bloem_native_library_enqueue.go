package scanqueue

import (
	"context"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

// The alias keeps the structural native queue contract independent of its caller.
type NativeLibraryAuthorizeTx = func(context.Context, pgx.Tx) error

func (s *Service) EnqueueNativeLibraryAuthorized(ctx context.Context, libraryID int, authorize NativeLibraryAuthorizeTx) (*models.ScanRun, bool, error) {
	return (&nativeLibraryEnqueue{service: s}).enqueue(ctx, libraryID, authorize)
}

// This invocation uses the existing service and repository. The per-invocation
// commit hook allows an actual commit followed by acknowledgement loss without
// changing shared queue state or relying on a process-global test switch.
type nativeLibraryEnqueue struct {
	service *Service
	commit  func(context.Context, pgx.Tx) error
}

func (q *nativeLibraryEnqueue) enqueue(ctx context.Context, libraryID int, authorize NativeLibraryAuthorizeTx) (*models.ScanRun, bool, error) {
	if q == nil || q.service == nil || q.service.repo == nil || q.service.repo.pool == nil || libraryID <= 0 || authorize == nil {
		return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	operationID := uuid.New()
	attemptedID := ulid.Make().String()
	for range 2 {
		run, created, retry, err := q.attempt(ctx, libraryID, authorize, operationID, attemptedID)
		if err != nil {
			return nil, false, err
		}
		if retry {
			continue
		}
		if created {
			q.service.publish(ctx, "scan.accepted", run)
		}
		return run, created, nil
	}
	return nil, false, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
}

func (q *nativeLibraryEnqueue) attempt(ctx context.Context, libraryID int, authorize NativeLibraryAuthorizeTx, operationID uuid.UUID, attemptedID string) (*models.ScanRun, bool, bool, error) {
	tx, err := q.service.repo.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, false, false, catalog.MapNativeOnboardingError(err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, false, false, catalog.MapNativeOnboardingError(err)
	}
	if err := authorize(ctx, tx); err != nil {
		return nil, false, false, err
	}

	// The callback has already authorized the retained library and source. Capture
	// their recovery identifiers before the commit attempt; ordinary local test
	// controls legitimately have no native marker.
	var creationKey uuid.UUID
	var sourceKey *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT n.creation_key,b.source_key FROM bloem_native_libraries n
 LEFT JOIN bloem_storage_bindings b ON b.folder_id=n.folder_id WHERE n.folder_id=$1`, libraryID).Scan(&creationKey, &sourceKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, false, catalog.MapNativeOnboardingError(err)
	}

	run, err := scanRunRow(tx.QueryRow(ctx,
		"INSERT INTO scan_runs(id,media_folder_id,mode,path,trigger,status,autoscan_event_id) "+
			"VALUES($1,$2,'library','','native_library_scan','accepted',NULL) "+
			"ON CONFLICT DO NOTHING RETURNING "+scanRunColumns, attemptedID, libraryID))
	created := err == nil
	if errors.Is(err, ErrScanRunNotFound) {
		run, err = q.service.repo.coalesceIntoActive(ctx, tx, CreateInput{
			LibraryID: libraryID, Mode: ModeLibrary, Path: "", Trigger: "native_library_scan",
		})
		if nativeQueueCoalesceRetry(run, err) {
			// No active selected row exists. Roll back and reacquire retained authority
			// in one fresh attempt rather than returning a fictitious coalesced run.
			return nil, false, true, nil
		}
	}
	if err != nil {
		return nil, false, false, catalog.MapNativeOnboardingError(err)
	}
	selectedID := run.ID
	if q.commit == nil {
		err = tx.Commit(ctx)
	} else {
		err = q.commit(ctx, tx)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return nil, false, false, catalog.MapNativeOnboardingError(err)
		}
		return nil, false, false, &catalog.MutationOutcomeUnknown{
			OperationID: operationID, CreationKey: creationKey, LibraryID: libraryID,
			SourceKey: sourceKey, ScanRunID: &selectedID, Operation: "scan", Cause: err,
		}
	}
	return run, created, false, nil
}

func nativeQueueCoalesceRetry(run *models.ScanRun, err error) bool {
	if errors.Is(err, ErrScanRunNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	return run == nil || (run.Status != StatusAccepted && run.Status != StatusRunning)
}
