package storagesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func validTTL(ttl time.Duration) bool { return ttl >= time.Millisecond && ttl <= 24*time.Hour }

// Begin fences any previous instance of the same owner as well as expired
// foreign owners. Source then run is the lock order for all journal operations.
func (r *Repository) Begin(ctx context.Context, sourceKey uuid.UUID, owner string, ttl time.Duration) (Lease, error) {
	lease := Lease{SourceKey: sourceKey, Owner: owner}
	if sourceKey == uuid.Nil || !validText(owner, 1024, true) || !validTTL(ttl) {
		return lease, fmt.Errorf("invalid storage scan lease")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return lease, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var root string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT configuration_revision,root_entry_id,enabled FROM bloem_storage_sources WHERE key=$1 FOR UPDATE`, sourceKey).Scan(&lease.ConfigurationRevision, &root, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !enabled) {
		return lease, ErrSourceUnavailable
	}
	if err != nil {
		return lease, err
	}
	var oldConfig int64
	var oldOwner string
	err = tx.QueryRow(ctx, `SELECT id,configuration_revision,lease_epoch,owner FROM bloem_storage_scan_runs WHERE source_key=$1 AND state='running' FOR UPDATE`, sourceKey).Scan(&lease.RunID, &oldConfig, &lease.Epoch, &oldOwner)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return lease, err
	}
	if err == nil && oldConfig == lease.ConfigurationRevision {
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
		_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES($1,$2,$3,'running',1,$4,clock_timestamp()+($5::bigint*interval '1 millisecond'))`, lease.RunID, sourceKey, lease.ConfigurationRevision, owner, ttl.Milliseconds())
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directories(run_id,directory_id) VALUES($1,$2)`, lease.RunID, root)
		}
	}
	if err != nil {
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
	err = tx.QueryRow(ctx, `SELECT source_key,configuration_revision,lease_epoch,owner,state FROM bloem_storage_scan_runs WHERE id=$1 FOR UPDATE`, lease.RunID).Scan(&actual.SourceKey, &actual.ConfigurationRevision, &actual.Epoch, &actual.Owner, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleLease
	}
	if err != nil {
		return err
	}
	if actual.SourceKey != lease.SourceKey || actual.ConfigurationRevision != lease.ConfigurationRevision || actual.Epoch != lease.Epoch || actual.Owner != lease.Owner || state != "running" {
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
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
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
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET state='complete',completed_at=clock_timestamp() WHERE id=$1`, lease.RunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
