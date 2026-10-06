package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// NativeConsumer is a host background worker, not an HTTP identity or authority.
// It retains one page/claim at a time and never reconciles absence or local paths.
type NativeConsumer struct {
	host      *nativestorage.Host
	sources   *storagesource.Repository
	resources *resourcetenancy.Store
	scanner   *scanner.Scanner
	leaseTTL  time.Duration
}

var _ NativeIngestor = (*NativeConsumer)(nil)

func NewNativeConsumer(host *nativestorage.Host, sources *storagesource.Repository, resources *resourcetenancy.Store, publisher *scanner.Scanner) (*NativeConsumer, error) {
	if host == nil || host.Registry == nil || host.Manager == nil || sources == nil || resources == nil || publisher == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	return &NativeConsumer{host: host, sources: sources, resources: resources, scanner: publisher, leaseTTL: time.Minute}, nil
}
func (c *NativeConsumer) HasNativeBinding(ctx context.Context, folderID int) (bool, error) {
	if c == nil || c.sources == nil {
		return false, storagesource.ErrSourceUnavailable
	}
	_, bound, err := c.sources.FolderBinding(ctx, folderID)
	return bound, err
}
func sameNativeConsumerSource(a, b storagesource.SourceConfig) bool {
	return a.Key == b.Key && a.OwnerID == b.OwnerID && a.InstallationID != nil && b.InstallationID != nil && *a.InstallationID == *b.InstallationID && a.PluginID == b.PluginID && a.ProviderSourceID == b.ProviderSourceID && a.RootEntryID == b.RootEntryID && a.ConfigurationRevision == b.ConfigurationRevision && a.Enabled && b.Enabled
}
func (c *NativeConsumer) approved(ctx context.Context, binding storagesource.Binding, expected storagesource.SourceConfig) (*plugins.NativeStorageSnapshot, error) {
	if err := c.resources.RequireNativeScan(ctx, binding, expected); err != nil {
		return nil, err
	}
	snapshot, err := c.host.Registry.Snapshot(ctx, expected.Key, expected.OwnerID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || !sameNativeConsumerSource(expected, snapshot.Source) || int64(snapshot.Installation.ID) != *expected.InstallationID || !snapshot.Installation.Enabled || snapshot.Generation == 0 {
		return nil, storagesource.ErrSourceUnavailable
	}
	return snapshot, nil
}
func sameNativeConsumerSnapshot(a, b *plugins.NativeStorageSnapshot) bool {
	return sameNativeConsumerSource(a.Source, b.Source) && a.Generation == b.Generation && a.ArtifactChecksum == b.ArtifactChecksum && a.Installation.ID == b.Installation.ID && a.Installation.InstallPath == b.Installation.InstallPath && proto.Equal(a.Manifest, b.Manifest) && proto.Equal(&publicv1.ConfigureRequest{Config: a.Config}, &publicv1.ConfigureRequest{Config: b.Config})
}

func nativeConsumerDescription(d *storagev1.DescribeResponse, source storagesource.SourceConfig) error {
	if d.GetRevision() != 1 || len(d.GetSources()) > 128 {
		return storagesource.ErrSourceUnavailable
	}
	found := 0
	for _, described := range d.GetSources() {
		if described.GetId() != source.ProviderSourceID {
			continue
		}
		found++
		if described.GetRootEntryId() != source.RootEntryID || !described.GetRevisionPinnedReads() {
			return storagesource.ErrSourceUnavailable
		}
	}
	if found != 1 {
		return storagesource.ErrSourceUnavailable
	}
	return nil
}

var errNativeRenewalStopped = errors.New("native renewal deliberately stopped")

// SQL may wrap cancellation. Only discard an error tree whose leaves are all
// context.Canceled; a joined deadline or genuine fence must survive Stop.
func nativeRenewalOnlyCanceled(err error) bool {
	if err == context.Canceled { //nolint:errorlint // Require exact cancellation leaves; errors.Is could hide a joined deadline/fence or a custom Is leaf.
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !nativeRenewalOnlyCanceled(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return nativeRenewalOnlyCanceled(wrapped.Unwrap())
	}
	return false
}

// startNativeRenewal covers long parsing/cover I/O. Stop cancels any outstanding
// SQL call and joins the worker before its caller releases runtime admission.
func startNativeRenewal(ctx context.Context, cancel context.CancelCauseFunc, interval time.Duration, renew func(context.Context) error) func() {
	renewalCtx, stop := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-renewalCtx.Done():
				return
			case <-ticker.C:
			}
			bounded, release := context.WithTimeout(renewalCtx, 10*time.Second)
			err := renew(bounded)
			cause := context.Cause(bounded)
			release()
			if err != nil {
				// Normal Stop only suppresses the cancellation it caused. A real
				// fence/provider/policy failure must never become full success just
				// because the caller concurrently finishes its last claim.
				if cause != errNativeRenewalStopped || !nativeRenewalOnlyCanceled(err) { //nolint:errorlint // Only this private Stop cause permits suppression; wrapped or equivalent causes must propagate.
					cancel(fmt.Errorf("native scan renewal: %w", err))
				}
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { stop(errNativeRenewalStopped); <-done }) }
}

// Fence retained host authority against the lease before any provider access or
// SQL policy callback. The repository independently verifies durable fencing.
func nativeConsumerLease(lease storagesource.IngestionLease, binding storagesource.Binding, source storagesource.SourceConfig) error {
	if lease.BindingID != binding.ID || lease.SourceKey != source.Key || binding.SourceKey != source.Key || lease.ConfigurationRevision != source.ConfigurationRevision {
		return storagesource.ErrStaleLease
	}
	return nil
}
func nativeConsumerClaim(claim storagesource.IngestionClaim, lease storagesource.IngestionLease, binding storagesource.Binding, source storagesource.SourceConfig) error {
	if err := nativeConsumerLease(claim.Lease, binding, source); err != nil {
		return err
	}
	if claim.Lease != lease || claim.Entry == nil || claim.Token == uuid.Nil {
		return storagesource.ErrStaleLease
	}
	return nil
}
func (c *NativeConsumer) IngestNativeFolder(ctx context.Context, requested *models.MediaFolder) (result *Result, err error) {
	result = &Result{ScanResult: &scanner.ScanResult{}}
	started := time.Now()
	defer func() {
		result.ScanDuration = time.Since(started)
		if err != nil {
			result.ScanResult.Errors++
		}
	}()
	if c == nil || requested == nil || requested.ID <= 0 {
		return result, storagesource.ErrReferenceConflict
	}
	// Runtime admission covers authorization, startup, discovery, parsing and all
	// files/renewal workers; Shutdown cannot reap a process under an active job.
	release, err := c.host.AcquireOpen()
	if err != nil {
		return result, err
	}
	defer release()
	binding, bound, err := c.sources.FolderBinding(ctx, requested.ID)
	if err != nil {
		return result, err
	}
	if !bound {
		return result, storagesource.ErrReferenceConflict
	}
	source, err := c.sources.Source(ctx, binding.SourceKey)
	if err != nil {
		return result, err
	}
	snapshot, err := c.approved(ctx, binding, source)
	if err != nil {
		return result, err
	}
	folder, err := c.scanner.NativeScanFolder(ctx, binding.FolderID)
	if err != nil {
		return result, err
	}
	if !librarykind.IsEbook(folder.Type) || !folder.Enabled {
		return result, storagesource.ErrReferenceConflict
	}
	for _, root := range folder.Paths {
		if strings.TrimSpace(root) != "" {
			return result, storagesource.ErrReferenceConflict
		}
	}
	// Repair committed metadata before runtime startup/provider I/O, including
	// when a previously completed run's backend cannot currently be started.
	c.scanner.ReconcileNativeEbookEnrichment(ctx, binding.FolderID)
	if err = ctx.Err(); err != nil {
		return result, err
	}
	session, err := c.host.Manager.Ensure(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return result, err
	}
	job, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopSession := context.AfterFunc(session.Context(), func() { cancel(storageplugin.ErrUnavailable) })
	defer stopSession()
	if session.Context().Err() != nil {
		cancel(storageplugin.ErrUnavailable)
	}
	defer func() {
		if cause := context.Cause(job); cause != nil {
			err = errors.Join(err, cause)
		}
	}()
	fresh, err := c.approved(job, binding, source)
	if err != nil {
		return result, err
	}
	if !sameNativeConsumerSnapshot(snapshot, fresh) {
		return result, storagesource.ErrSourceUnavailable
	}
	describeCtx, stopDescribe := context.WithTimeout(job, 10*time.Second)
	description, err := session.Provider().Describe(describeCtx, &storagev1.DescribeRequest{}, grpc.MaxCallRecvMsgSize(1<<20))
	stopDescribe()
	if err != nil {
		return result, err
	}
	if err = nativeConsumerDescription(description, source); err != nil {
		return result, err
	}
	recheck := func(ctx context.Context) error {
		current, err := c.approved(ctx, binding, source)
		if err != nil {
			return err
		}
		if !sameNativeConsumerSnapshot(snapshot, current) {
			return storagesource.ErrSourceUnavailable
		}
		return nil
	}
	owner := "native-consumer:" + uuid.NewString()
	run, pending, err := c.sources.PendingIngestionRun(job, binding.ID)
	if err != nil {
		return result, err
	}
	if !pending {
		discovery, err := c.sources.Begin(job, source.Key, owner, c.leaseTTL)
		if err != nil {
			return result, err
		}
		if discovery.SourceKey != source.Key || discovery.ConfigurationRevision != source.ConfigurationRevision {
			return result, storagesource.ErrStaleLease
		}
		stopRenew := startNativeRenewal(job, cancel, c.leaseTTL/3, func(ctx context.Context) error {
			if err := recheck(ctx); err != nil {
				return err
			}
			return c.sources.Renew(ctx, discovery, c.leaseTTL)
		})
		defer stopRenew()
		for {
			if err = job.Err(); err != nil {
				return result, err
			}
			if err = c.resources.RequireNativeScan(job, binding, source); err != nil {
				return result, err
			}
			_, more, err := c.sources.NextDirectory(job, discovery)
			if err != nil {
				return result, err
			}
			if !more {
				// Stop before transitioning state, so a renewal cannot race Complete and
				// mistake a legitimately completed discovery for lease loss.
				stopRenew()
				if err = c.sources.Complete(job, discovery); err != nil {
					return result, err
				}
				break
			}
			if _, err = c.sources.DiscoverPage(job, discovery, session.Provider()); err != nil {
				return result, err
			}
		}
		run = discovery.RunID
	}
	if err = recheck(job); err != nil {
		return result, err
	}
	lease, err := c.sources.BeginIngestion(job, run, binding.ID, owner, c.leaseTTL)
	if err != nil {
		return result, err
	}
	if err = nativeConsumerLease(lease, binding, source); err != nil {
		return result, err
	}
	stopRenew := startNativeRenewal(job, cancel, c.leaseTTL/3, func(ctx context.Context) error {
		if err := recheck(ctx); err != nil {
			return err
		}
		return c.sources.RenewIngestion(ctx, lease, c.leaseTTL)
	})
	defer stopRenew()
	media := mediasource.NewPluginSource(session.Provider())
	processed := 0
	for {
		if err = job.Err(); err != nil {
			return result, err
		}
		if err = c.resources.RequireNativeScan(job, binding, source); err != nil {
			return result, err
		}
		claim, more, err := c.sources.NextIngestion(job, lease)
		if err != nil {
			return result, err
		}
		if !more {
			break
		}
		if err = nativeConsumerClaim(claim, lease, binding, source); err != nil {
			return result, err
		}
		// Every acknowledgement, including unsupported formats, uses mandatory
		// SQL authority before the repository takes its source/run/claim locks.
		var outcome string
		authorize := func(ctx context.Context, tx pgx.Tx) error {
			if err := nativeConsumerClaim(claim, lease, binding, source); err != nil {
				return err
			}
			if err := c.resources.RequireNativeScanTx(ctx, tx, binding, source); err != nil {
				return err
			}
			var generation uint64
			if err := tx.QueryRow(ctx, "SELECT runtime_generation FROM plugin_installations WHERE id=$1", *source.InstallationID).Scan(&generation); err != nil {
				return err
			}
			if generation != snapshot.Generation {
				return storagesource.ErrSourceUnavailable
			}
			location, err := storagesource.CatalogLocation(binding.ID, claim.Entry.Id)
			if err != nil {
				return err
			}
			var revision *string
			err = tx.QueryRow(ctx, `SELECT r.revision FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id AND r.binding_id=$2 AND r.entry_id=$3 WHERE f.media_folder_id=$1 AND f.file_path=$4`, binding.FolderID, binding.ID, claim.Entry.Id, location).Scan(&revision)
			if errors.Is(err, pgx.ErrNoRows) {
				outcome = "new"
				return nil
			}
			if err != nil {
				return err
			}
			outcome = "updated"
			if revision != nil && *revision == claim.Entry.Revision {
				outcome = "unchanged"
			}
			return nil
		}
		suffix := strings.ToLower(path.Ext(claim.Entry.Name))
		if suffix != ".epub" && suffix != ".pdf" {
			if err = c.sources.PublishAuthorizedIngestion(job, claim, authorize, nil); err != nil {
				return result, err
			}
			// Existing ScanResult uses Unchanged for explicitly skipped files.
			result.ScanResult.Unchanged++
		} else {
			sidecars, err := scanner.ResolveNativeEbookSidecars(job, c.sources, claim, func(ctx context.Context, ref storagesource.PersistedRef) (mediasource.File, error) {
				if ref.BindingID != binding.ID {
					return nil, storagesource.ErrReferenceConflict
				}
				return mediasource.Open(ctx, media, mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: ref.EntryID, Revision: ref.Revision})
			})
			if err != nil {
				return result, err
			}
			file, err := mediasource.Open(job, media, mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: claim.Entry.Id, Revision: claim.Entry.Revision})
			if err != nil {
				return result, err
			}
			id, publishErr := c.scanner.PublishAuthorizedNativeEbook(job, c.sources, claim, folder, file, sidecars, authorize)
			closeErr := file.Close()
			// A nonempty ID denotes a committed catalog write even if subsequent
			// enrichment fails. Report that actual work, then fail the overall job.
			if publishErr == nil || id != "" {
				switch outcome {
				case "new":
					result.ScanResult.New++
				case "updated":
					result.ScanResult.Updated++
				case "unchanged":
					result.ScanResult.Unchanged++
				}
			}
			if err = errors.Join(publishErr, closeErr); err != nil {
				return result, err
			}
		}
		processed++
		reportProgress(job, ProgressUpdate{Phase: "scanning", FilesDiscovered: processed, FilesProcessed: processed, New: result.ScanResult.New, Updated: result.ScanResult.Updated, Unchanged: result.ScanResult.Unchanged})
	}
	stopRenew()
	if err = recheck(job); err != nil {
		return result, err
	}
	c.scanner.ReconcileNativeEbookEnrichment(job, binding.FolderID)
	if err = job.Err(); err != nil {
		return result, err
	}
	return result, nil
}
