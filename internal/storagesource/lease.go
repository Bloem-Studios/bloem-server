package storagesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func validTTL(ttl time.Duration) bool { return ttl >= time.Millisecond && ttl <= 24*time.Hour }

// Begin fences any previous instance of the same owner as well as expired
// foreign owners. Source then run is the lock order for all journal operations.
func (r *Repository) Begin(ctx context.Context, sourceKey uuid.UUID, owner string, ttl time.Duration) (Lease, error) {
	return r.begin(ctx, sourceKey, owner, ttl, nil)
}

func (r *Repository) begin(ctx context.Context, sourceKey uuid.UUID, owner string, ttl time.Duration, authorize BeginAuthorizer) (Lease, error) {
	lease := Lease{SourceKey: sourceKey, Owner: owner}
	if sourceKey == uuid.Nil || !validText(owner, 1024, true) || !validTTL(ttl) {
		return lease, fmt.Errorf("invalid storage scan lease")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return lease, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if authorize != nil {
		if err = authorize(ctx, tx); err != nil {
			return lease, err
		}
	}
	var root string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT configuration_revision,root_entry_id,enabled FROM bloem_storage_sources WHERE key=$1 FOR UPDATE`, sourceKey).Scan(&lease.ConfigurationRevision, &root, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !enabled) {
		return lease, ErrSourceUnavailable
	}
	if err != nil {
		return lease, err
	}
	// Retain the binding before locking its run. Waiting here could invert the
	// deletion trigger's location -> run order while Begin owns a prior run.
	locations, err := tx.Query(ctx, `SELECT id,selection_revision FROM library_storage_locations WHERE source_key=$1 ORDER BY id LIMIT 2 FOR KEY SHARE NOWAIT`, sourceKey)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return lease, ErrStaleLease
		}
		return lease, err
	}
	count := 0
	for locations.Next() {
		count++
		if err := locations.Scan(&lease.LocationID, &lease.SelectionRevision); err != nil {
			locations.Close()
			return lease, err
		}
	}
	err = locations.Err()
	locations.Close()
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return lease, ErrStaleLease
		}
		return lease, err
	}
	if count != 1 {
		return lease, ErrReferenceConflict
	}
	var oldConfig int64
	var oldOwner string
	var oldLocation uuid.UUID
	var oldSelection int64
	err = tx.QueryRow(ctx, `SELECT id,configuration_revision,lease_epoch,owner,location_id,selection_revision FROM bloem_storage_scan_runs WHERE source_key=$1 AND state='running' FOR UPDATE`, sourceKey).Scan(&lease.RunID, &oldConfig, &lease.Epoch, &oldOwner, &oldLocation, &oldSelection)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return lease, err
	}
	if err == nil && oldConfig == lease.ConfigurationRevision && oldLocation == lease.LocationID && oldSelection == lease.SelectionRevision {
		var active bool
		if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&active); err != nil {
			return lease, err
		}
		if active && oldOwner != owner {
			return lease, ErrStaleLease
		}
		lease.Epoch++
		_, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET owner=$2,lease_epoch=$3,lease_until=clock_timestamp()+($4::bigint*interval '1 millisecond') WHERE id=$1`, lease.RunID, owner, lease.Epoch, ttl.Milliseconds())
	} else {
		if lease.RunID != uuid.Nil {
			if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET state='failed' WHERE id=$1`, lease.RunID); err != nil {
				return lease, err
			}
		}
		lease.RunID = uuid.New()
		lease.Epoch = 1
		_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until,location_id,selection_revision) VALUES($1,$2,$3,'running',1,$4,clock_timestamp()+($5::bigint*interval '1 millisecond'),$6,$7)`, lease.RunID, sourceKey, lease.ConfigurationRevision, owner, ttl.Milliseconds(), lease.LocationID, lease.SelectionRevision)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directories(run_id,directory_id) VALUES($1,$2)`, lease.RunID, root)
		}
	}
	if err != nil {
		return lease, err
	}
	// Fence ingestion of a previous completed generation before any new pages
	// can overwrite its staged entries. The pointer and run commit together.
	if _, err = tx.Exec(ctx, "UPDATE bloem_storage_sources SET discovery_run_id=$2 WHERE key=$1", sourceKey, lease.RunID); err != nil {
		return lease, err
	}
	return lease, tx.Commit(ctx)
}

func verifyLease(ctx context.Context, tx pgx.Tx, lease Lease) error {
	// Acquire locks before checking database clock expiry: a query that waits for
	// a lock must not use an expiry predicate evaluated before that wait.
	var configRevision int64
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT configuration_revision,enabled FROM bloem_storage_sources WHERE key=$1 FOR UPDATE`, lease.SourceKey).Scan(&configRevision, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleLease
	}
	if err != nil {
		return err
	}
	if !enabled || configRevision != lease.ConfigurationRevision {
		return ErrStaleLease
	}
	var actual Lease
	var state string
	var currentSelection bool
	err = tx.QueryRow(ctx, `SELECT r.source_key,r.configuration_revision,r.lease_epoch,r.owner,r.state,r.location_id,r.selection_revision,
	 EXISTS(SELECT 1 FROM library_storage_locations l WHERE l.id=r.location_id AND l.source_key=r.source_key AND l.selection_revision=r.selection_revision)
	 FROM bloem_storage_scan_runs r WHERE r.id=$1 FOR UPDATE`, lease.RunID).Scan(&actual.SourceKey, &actual.ConfigurationRevision, &actual.Epoch, &actual.Owner, &state, &actual.LocationID, &actual.SelectionRevision, &currentSelection)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleLease
	}
	if err != nil {
		return err
	}
	if actual.SourceKey != lease.SourceKey || actual.ConfigurationRevision != lease.ConfigurationRevision || actual.Epoch != lease.Epoch || actual.Owner != lease.Owner || state != "running" || actual.LocationID != lease.LocationID || actual.SelectionRevision != lease.SelectionRevision || !currentSelection {
		return ErrStaleLease
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrStaleLease
	}
	return nil
}

func (r *Repository) Renew(ctx context.Context, lease Lease, ttl time.Duration) error {
	if !validTTL(ttl) {
		return fmt.Errorf("invalid storage lease duration")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()+($2::bigint*interval '1 millisecond') WHERE id=$1`, lease.RunID, ttl.Milliseconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Complete records successful discovery only. Missing-file reconciliation is a
// later catalog ingestion concern, never inferred from an interrupted journal.
func (r *Repository) Complete(ctx context.Context, lease Lease) error {
	return r.complete(ctx, lease, nil, nil)
}

func (r *Repository) complete(ctx context.Context, lease Lease, authorize CompletionAuthorizer, finalize CompletionFinalizer) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if authorize != nil {
		if err = authorize(ctx, tx); err != nil {
			return err
		}
	}
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_scan_directories WHERE run_id=$1 AND NOT complete)`, lease.RunID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return ErrCheckpointConflict
	}
	if finalize != nil {
		if err = finalize(ctx, tx); err != nil {
			return err
		}
	}
	// Catalog cleanup may wait for rows. Recheck the database clock after those
	// waits, before committing any cleanup or marking this generation complete.
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET state='complete',completed_at=clock_timestamp() WHERE id=$1`, lease.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
