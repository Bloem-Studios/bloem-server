package storagesource

import (
	"context"
	"errors"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// IngestionLease operates on a completed discovery generation, independently of
// its discovery lease. All callers must already have host source/library authority.
type IngestionLease struct {
	RunID, BindingID, SourceKey  uuid.UUID
	ConfigurationRevision, Epoch int64
	Owner                        string
}
type IngestionClaim struct {
	Lease IngestionLease
	Token uuid.UUID
	Entry *storagev1.Entry
}
type ingestionCheckpoint struct {
	last, pending, revision string
	token                   *uuid.UUID
	complete                bool
}

// BeginIngestion resumes a binding's committed generation without restarting
// discovery. Same-owner takeover increases the epoch and replaces pending tokens.
func (r *Repository) BeginIngestion(ctx context.Context, runID, bindingID uuid.UUID, owner string, ttl time.Duration) (IngestionLease, error) {
	l := IngestionLease{RunID: runID, BindingID: bindingID, Owner: owner}
	if runID == uuid.Nil || bindingID == uuid.Nil || !validText(owner, 1024, true) || !validTTL(ttl) {
		return l, ErrStaleLease
	}
	if err := r.pool.QueryRow(ctx, "SELECT source_key FROM bloem_storage_bindings WHERE id=$1", bindingID).Scan(&l.SourceKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return l, ErrReferenceConflict
		}
		return l, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return l, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = verifyIngestionGeneration(ctx, tx, &l, true); err != nil {
		return l, err
	}
	var priorOwner string
	var active bool
	err = tx.QueryRow(ctx, "SELECT owner,lease_epoch FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2 FOR UPDATE", runID, bindingID).Scan(&priorOwner, &l.Epoch)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return l, err
	}
	if err == nil {
		if err = tx.QueryRow(ctx, "SELECT lease_until>clock_timestamp() FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", runID, bindingID).Scan(&active); err != nil {
			return l, err
		}
		if active && priorOwner != owner {
			return l, ErrStaleLease
		}
		l.Epoch++
		_, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET lease_epoch=$3,owner=$4,lease_until=clock_timestamp()+($5::bigint*interval '1 millisecond'),pending_token=CASE WHEN pending_token IS NULL THEN NULL ELSE $6::uuid END WHERE run_id=$1 AND binding_id=$2", runID, bindingID, l.Epoch, owner, ttl.Milliseconds(), uuid.New())
	} else {
		l.Epoch = 1
		_, err = tx.Exec(ctx, "INSERT INTO bloem_storage_ingestion(run_id,binding_id,lease_epoch,owner,lease_until) VALUES($1,$2,1,$3,clock_timestamp()+($4::bigint*interval '1 millisecond'))", runID, bindingID, owner, ttl.Milliseconds())
	}
	if err != nil {
		return l, err
	}
	return l, tx.Commit(ctx)
}

// Lock order is source, completed discovery run, then ingestion checkpoint.
// Config and generation are rechecked after source locking, never from a cache.
func verifyIngestionGeneration(ctx context.Context, tx pgx.Tx, l *IngestionLease, initialize bool) error {
	var revision int64
	var enabled bool
	var currentRun *uuid.UUID
	err := tx.QueryRow(ctx, "SELECT configuration_revision,enabled,discovery_run_id FROM bloem_storage_sources WHERE key=$1 FOR UPDATE", l.SourceKey).Scan(&revision, &enabled, &currentRun)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleLease
	}
	if err != nil {
		return err
	}
	if !enabled || currentRun == nil || *currentRun != l.RunID || (!initialize && revision != l.ConfigurationRevision) {
		return ErrStaleLease
	}
	var runRevision int64
	var state string
	err = tx.QueryRow(ctx, "SELECT configuration_revision,state FROM bloem_storage_scan_runs WHERE id=$1 AND source_key=$2 FOR UPDATE", l.RunID, l.SourceKey).Scan(&runRevision, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleLease
	}
	if err != nil {
		return err
	}
	if state != "complete" || runRevision != revision {
		return ErrStaleLease
	}
	var bindingExists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE id=$1 AND source_key=$2)", l.BindingID, l.SourceKey).Scan(&bindingExists); err != nil {
		return err
	}
	if !bindingExists {
		return ErrReferenceConflict
	}
	l.ConfigurationRevision = revision
	return nil
}
func verifyIngestionLease(ctx context.Context, tx pgx.Tx, l IngestionLease) (ingestionCheckpoint, error) {
	var checkpoint ingestionCheckpoint
	if err := verifyIngestionGeneration(ctx, tx, &l, false); err != nil {
		return checkpoint, err
	}
	var epoch int64
	var owner string
	err := tx.QueryRow(ctx, "SELECT lease_epoch,owner,last_entry_id,pending_entry_id,pending_revision,pending_token,complete FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2 FOR UPDATE", l.RunID, l.BindingID).Scan(&epoch, &owner, &checkpoint.last, &checkpoint.pending, &checkpoint.revision, &checkpoint.token, &checkpoint.complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, ErrStaleLease
	}
	if err != nil {
		return checkpoint, err
	}
	if epoch != l.Epoch || owner != l.Owner {
		return checkpoint, ErrStaleLease
	}
	if err = ingestionLeaseActive(ctx, tx, l); err != nil {
		return checkpoint, err
	}
	return checkpoint, nil
}
func ingestionLeaseActive(ctx context.Context, tx pgx.Tx, l IngestionLease) error {
	var active bool
	if err := tx.QueryRow(ctx, "SELECT lease_until>clock_timestamp() FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrStaleLease
	}
	return nil
}
func loadIngestionEntry(ctx context.Context, tx pgx.Tx, l IngestionLease, id string) (*storagev1.Entry, error) {
	entry := &storagev1.Entry{Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	err := tx.QueryRow(ctx, "SELECT entry_id,name,logical_path,size,modified_unix_nano,revision FROM bloem_storage_entries WHERE source_key=$1 AND last_seen_run=$2 AND configuration_revision=$3 AND entry_id=$4 AND kind=1 FOR UPDATE", l.SourceKey, l.RunID, l.ConfigurationRevision, id).Scan(&entry.Id, &entry.Name, &entry.LogicalPath, &entry.Size, &entry.ModifiedUnixNano, &entry.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckpointConflict
	}
	return entry, err
}

// NextIngestion returns at most one pinned entry and persists its claim before
// returning. Repeated requests return that claim until successful publication.
// The immutable completed run makes keyset iteration safe across restarts.
func (r *Repository) NextIngestion(ctx context.Context, l IngestionLease) (IngestionClaim, bool, error) {
	claim := IngestionClaim{Lease: l}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return claim, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	checkpoint, err := verifyIngestionLease(ctx, tx, l)
	if err != nil {
		return claim, false, err
	}
	if checkpoint.complete {
		return claim, false, tx.Commit(ctx)
	}
	id := checkpoint.pending
	if id == "" {
		err = tx.QueryRow(ctx, "SELECT entry_id FROM bloem_storage_entries WHERE source_key=$1 AND last_seen_run=$2 AND configuration_revision=$3 AND kind=1 AND entry_id>$4 ORDER BY entry_id LIMIT 1", l.SourceKey, l.RunID, l.ConfigurationRevision, checkpoint.last).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET complete=true WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID); err != nil {
				return claim, false, err
			}
			return claim, false, tx.Commit(ctx)
		}
		if err != nil {
			return claim, false, err
		}
	}
	claim.Entry, err = loadIngestionEntry(ctx, tx, l, id)
	if err != nil {
		return claim, false, err
	}
	if !validText(claim.Entry.GetRevision(), 4096, true) {
		return claim, false, ErrCheckpointConflict
	}
	if checkpoint.token != nil {
		if checkpoint.revision != claim.Entry.GetRevision() {
			return claim, false, ErrCheckpointConflict
		}
		claim.Token = *checkpoint.token
	} else {
		claim.Token = uuid.New()
		if _, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET pending_entry_id=$3,pending_revision=$4,pending_token=$5 WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID, id, claim.Entry.GetRevision(), claim.Token); err != nil {
			return claim, false, err
		}
	}
	return claim, true, tx.Commit(ctx)
}

// PublishIngestion atomically publishes host-owned catalog writes and advances
// the durable cursor. The callback may call FileRepository.UpsertTx and
// AttachFileTx, but must not commit the transaction or perform provider/network
// I/O. A nil callback explicitly acknowledges an unsupported entry without
// publishing it. Parsing/cover failures must not call this method.
func (r *Repository) PublishIngestion(ctx context.Context, claim IngestionClaim, publish func(context.Context, pgx.Tx, *storagev1.Entry) error) error {
	if claim.Entry == nil || claim.Token == uuid.Nil {
		return ErrCheckpointConflict
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	l := claim.Lease
	checkpoint, err := verifyIngestionLease(ctx, tx, l)
	if err != nil {
		return err
	}
	if checkpoint.complete || checkpoint.token == nil || *checkpoint.token != claim.Token || checkpoint.pending != claim.Entry.GetId() || checkpoint.revision != claim.Entry.GetRevision() {
		return ErrCheckpointConflict
	}
	entry, err := loadIngestionEntry(ctx, tx, l, checkpoint.pending)
	if err != nil {
		return err
	}
	if !proto.Equal(entry, claim.Entry) {
		return ErrCheckpointConflict
	}
	if publish != nil {
		if err = publish(ctx, tx, entry); err != nil {
			return err
		}
	}
	if err = ingestionLeaseActive(ctx, tx, l); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET last_entry_id=pending_entry_id,pending_entry_id='',pending_revision='',pending_token=NULL WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) RenewIngestion(ctx context.Context, l IngestionLease, ttl time.Duration) error {
	if !validTTL(ttl) {
		return ErrStaleLease
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = verifyIngestionLease(ctx, tx, l); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()+($3::bigint*interval '1 millisecond') WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID, ttl.Milliseconds())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
