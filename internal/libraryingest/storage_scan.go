package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/cache"
	"github.com/Silo-Server/silo-server/internal/librarykind"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// StorageIngestor scans libraries whose location is a storage source.
type StorageIngestor interface {
	FolderLocation(context.Context, int) (storagesource.Location, bool, error)
	ScanStorageFolder(context.Context, *models.MediaFolder, storagesource.Location) (*Result, error)
}

// SetStorageIngestor is startup-only, before scans are admitted.
func (e *Executor) SetStorageIngestor(storage StorageIngestor) {
	if e != nil {
		e.storageIngestor = storage
	}
}

// tryStorageIngest scans a library with a storage location and reports whether
// it did. Libraries without one continue with the filesystem scanner.
func (e *Executor) tryStorageIngest(ctx context.Context, folder *models.MediaFolder, mode scopeMode) (*Result, bool, error) {
	if e == nil || folder == nil || e.storageIngestor == nil {
		return nil, false, nil
	}
	location, ok, err := e.storageIngestor.FolderLocation(ctx, folder.ID)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, false, nil
	}
	if mode != scopeModeLibrary {
		return nil, true, fmt.Errorf("storage libraries scan only as a whole library")
	}
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := e.storageIngest(scanCtx, cancel, folder, location)
	return result, true, storageScanOutcome(scanCtx, folder, location, err)
}

func (e *Executor) storageIngest(scanCtx context.Context, cancel context.CancelFunc, folder *models.MediaFolder, location storagesource.Location) (*Result, error) {
	entry, err := e.begin(scanCtx, scopeClaim{folderID: folder.ID, mode: scopeModeLibrary}, cancel)
	if err != nil {
		return nil, err
	}
	defer e.finish(entry)
	reportProgress(scanCtx, ProgressUpdate{Phase: "preparing", Message: "Preparing storage scan"})
	result, err := e.storageIngestor.ScanStorageFolder(scanCtx, folder, location)
	if err != nil {
		return result, err
	}
	if e.folders != nil {
		if err = e.folders.UpdateLastScanned(scanCtx, folder.ID, e.now().UTC()); err != nil {
			return result, fmt.Errorf("update storage library last scanned: %w", err)
		}
	}
	if shouldPublish(result) && e.events != nil {
		if err = e.events.Publish(scanCtx, cache.ChannelCatalog, cache.Event{Type: cache.EventScanComplete, Payload: strconv.Itoa(folder.ID)}); err != nil {
			return result, err
		}
	}
	if shouldPublish(result) && e.realtime != nil {
		if err = e.realtime.PublishCatalogLibraryChanged(scanCtx, notifications.LibraryChangeEvent{
			LibraryID: folder.ID, Reason: "scan",
			New:     scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.New }),
			Updated: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Updated }),
			Missing: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Missing }),
		}); err != nil {
			return result, err
		}
	}
	reportProgress(scanCtx, ProgressUpdate{Phase: "completed", Message: "Storage scan completed"})
	return result, nil
}

// storageScanOutcome decides how the scan queue reports a storage scan. The
// queue reports any error tree containing context.Canceled as an operator
// cancellation, without a message. Provider streams, tool bridges and the
// scan's own job context report their teardown that way too, so only the
// cancellation of scanCtx itself (an operator cancel, a superseding claim or
// shutdown) is reported as cancelled. Every other failure is reported failed,
// with its message and a log line.
func storageScanOutcome(scanCtx context.Context, folder *models.MediaFolder, location storagesource.Location, err error) error {
	if err == nil {
		return nil
	}
	if canceled := scanCtx.Err(); canceled != nil {
		slog.InfoContext(scanCtx, "storage scan canceled", "component", "libraryingest", "library_id", folder.ID, "storage_source", location.SourceKey)
		if errors.Is(canceled, context.Canceled) && !errors.Is(err, context.Canceled) {
			// A provider may report the cancellation as its own status error.
			err = errors.Join(err, canceled)
		}
		return err
	}
	failure := &storageScanFailure{cause: err}
	slog.ErrorContext(scanCtx, "storage scan failed", "component", "libraryingest", "library_id", folder.ID, "storage_source", location.SourceKey, "error", failure.Error())
	return failure
}

// storageScanFailure is a storage scan failure while the scan was not
// canceled. It hides context.Canceled leaves from classification, because they
// come from internal teardown, and keeps every other cause available to
// errors.Is and errors.As.
type storageScanFailure struct{ cause error }

func (e *storageScanFailure) Error() string {
	return "storage scan failed: " + strings.ReplaceAll(e.cause.Error(), "\n", "; ")
}

func (e *storageScanFailure) Is(target error) bool {
	// Hide exactly the sentinel the scan queue treats as an operator cancel.
	return target != context.Canceled && errors.Is(e.cause, target) //nolint:errorlint // Identity with the sentinel is the intended check.
}

func (e *storageScanFailure) As(target any) bool { return errors.As(e.cause, target) }

// StorageScanner lists a storage source and publishes each listed page to its
// library's catalog in the page's own transaction. Books with provider
// metadata publish without their files being read; others are parsed. Admitted
// movies are probed through ScanReader before that authorized transaction.
type StorageScanner struct {
	host             *nativestorage.Host
	sources          *storagesource.Repository
	resources        *resourcetenancy.Store
	scanner          *scanner.Scanner
	leaseTTL         time.Duration
	nativeMovieReady bool
	movieReader      *nativestorage.ScanReader
	movieProbeConfig scanner.NativeProbeConfig
	movieMetadata    NativeMovieMetadata
}

var _ StorageIngestor = (*StorageScanner)(nil)

func NewStorageScanner(host *nativestorage.Host, sources *storagesource.Repository, resources *resourcetenancy.Store, publisher *scanner.Scanner) (*StorageScanner, error) {
	if host == nil || host.Registry == nil || host.Manager == nil || sources == nil || resources == nil || publisher == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	return &StorageScanner{host: host, sources: sources, resources: resources, scanner: publisher, leaseTTL: time.Minute}, nil
}

func (c *StorageScanner) FolderLocation(ctx context.Context, folderID int) (storagesource.Location, bool, error) {
	return c.sources.FolderLocation(ctx, folderID)
}

func (c *StorageScanner) ScanStorageFolder(ctx context.Context, folder *models.MediaFolder, location storagesource.Location) (result *Result, err error) {
	result = &Result{ScanResult: &scanner.ScanResult{}}
	started := time.Now()
	defer func() {
		result.ScanDuration = time.Since(started)
		if err != nil {
			result.ScanResult.Errors++
		}
	}()
	if folder == nil || folder.ID != location.FolderID {
		return result, storagesource.ErrReferenceConflict
	}
	if !librarykind.SupportsNativeStorage(folder.Type, c.nativeMovieReady) || !folder.Enabled {
		return result, fmt.Errorf("storage library kind is not ready")
	}
	// Runtime admission covers every provider call and renewal worker; Shutdown
	// cannot reap a plugin process under an active scan.
	release, err := c.host.AcquireOpen()
	if err != nil {
		return result, err
	}
	defer release()
	source, err := c.sources.Source(ctx, location.SourceKey)
	if err != nil {
		return result, err
	}
	if err = c.resources.RequireStorageScan(ctx, location, source); err != nil {
		return result, err
	}
	snapshot, err := c.snapshot(ctx, source)
	if err != nil {
		return result, err
	}
	session, err := c.host.Manager.Ensure(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return result, err
	}
	// Movie matching runs behind the listing. Registered before the job's
	// cancel, so an early return cancels in-flight matches before joining them.
	var matching sync.WaitGroup
	defer matching.Wait()
	matchStats := &nativeMovieMatchStats{}
	// One attempt per item per scan: a later page's copy of a title whose
	// match just failed waits for the next scan.
	matchAttempted := make(map[string]bool)
	job, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopSession := context.AfterFunc(session.Context(), func() { cancel(storageplugin.ErrUnavailable) })
	defer stopSession()
	defer func() {
		if cause := context.Cause(job); cause != nil && !errors.Is(cause, context.Canceled) {
			if storageRenewalOnlyCanceled(err) {
				// The job's cancellation is a consequence of cause, such as a
				// failed renewal or a lost plugin session; report the cause.
				err = cause
			} else {
				err = errors.Join(err, cause)
			}
		}
	}()
	provider := session.Provider()
	description, err := storageplugin.Describe(job, provider)
	if err != nil {
		return result, err
	}
	if err = describedSource(description, source, snapshot); err != nil {
		return result, err
	}
	// Describe/startup may outlive a remote configuration or authorization
	// change. Recheck before claiming a lease, publishing, or sweeping files.
	fresh, err := c.sources.Source(job, source.Key)
	if err != nil {
		return result, err
	}
	if !nativestorage.SameSource(source, fresh) {
		return result, storagesource.ErrSourceUnavailable
	}
	if err = c.resources.RequireStorageScan(job, location, fresh); err != nil {
		return result, err
	}
	freshSnapshot, err := c.snapshot(job, fresh)
	if err != nil {
		return result, err
	}
	if freshSnapshot.Generation != snapshot.Generation || freshSnapshot.ArtifactChecksum != snapshot.ArtifactChecksum {
		return result, storagesource.ErrSourceUnavailable
	}

	authorize := func(ctx context.Context, tx pgx.Tx) error {
		return c.resources.RequireStorageScanTx(ctx, tx, location, source)
	}
	lease, err := c.sources.BeginAuthorized(job, source.Key, "storage-scan:"+uuid.NewString(), c.leaseTTL, authorize)
	if err != nil {
		return result, err
	}
	stopRenew := func() {}
	defer func() {
		// A scan that stops without completing releases its claim, so a retry
		// resumes the journal at once instead of failing as a stale lease
		// until the claim expires. Renewal and every lease user have joined.
		if err != nil {
			stopRenew()
			c.releaseLease(ctx, lease)
		}
	}()
	if lease.ConfigurationRevision != source.ConfigurationRevision {
		return result, storagesource.ErrStaleLease
	}
	stopRenew = startStorageRenewal(job, cancel, c.leaseTTL/3, func(ctx context.Context) error {
		if err := c.resources.RequireStorageScan(ctx, location, source); err != nil {
			return err
		}
		return c.sources.Renew(ctx, lease, c.leaseTTL)
	})
	defer stopRenew()

	media := mediasource.NewPluginSource(provider)
	skipDirectory := func(entry *storagev1.Entry) bool { return scanner.StorageDirectorySkipped(folder.Type, entry) }
	listed := 0
	for {
		if err = job.Err(); err != nil {
			return result, err
		}
		checkpoint, more, err := c.sources.NextDirectory(job, lease)
		if err != nil {
			return result, err
		}
		if !more {
			// Matches commit catalog changes; finish them before the sweep.
			matching.Wait()
			// Stop renewing first so a renewal cannot race completion.
			stopRenew()
			var trashed, unlisted int
			var orphanedImageDirs []string
			finalize := func(ctx context.Context, tx pgx.Tx) error {
				// Absence is inferred only here, once every listing completed.
				listed, err := storagesource.FullListingsTx(ctx, tx, lease)
				if err != nil {
					return err
				}
				if unlisted, err = c.scanner.MarkUnlistedStorageFilesTx(ctx, tx, location, listed); err != nil {
					return err
				}
				trashed, orphanedImageDirs, err = c.scanner.SweepStorageLibraryTx(ctx, tx, folder)
				return err
			}
			if err = c.sources.CompleteAuthorized(job, lease, authorize, finalize); err != nil {
				return result, err
			}
			result.ScanResult.Missing += unlisted
			result.ScanResult.FilesDeleted += trashed
			c.scanner.CleanupStorageLibraryArtwork(job, orphanedImageDirs)
			matchStats.log(job, folder.ID)
			return result, nil
		}
		page, err := c.sources.FetchPage(job, lease, &checkpoint, provider)
		if err != nil {
			return result, err
		}
		var books []scanner.StorageEbook
		var movies []scanner.StorageMovie
		var skipped int
		var excluded []string
		if librarykind.IsMovie(folder.Type) {
			movies, skipped, excluded, err = c.prepareMovies(job, location, source, lease, checkpoint, page)
		} else {
			books, skipped, err = c.prepare(job, media, source, page.GetEntries())
		}
		if err != nil {
			return result, err
		}
		// The previous page's matches may move items to their provider
		// identity; publish against settled catalog rows.
		matching.Wait()
		var published scanner.StoragePublishResult
		var matches []scanner.StorageMovieMatch
		var removedFiles int
		publish := func(ctx context.Context, tx pgx.Tx, _ []*storagev1.Entry, removed []string) error {
			var err error
			if librarykind.IsMovie(folder.Type) {
				var moviePage scanner.StorageMoviePublishResult
				moviePage, err = c.scanner.PublishStorageMoviesTx(ctx, tx, folder, location, source.ConfigurationRevision, movies)
				published, matches = moviePage.StoragePublishResult, moviePage.Matches
				// A file an older scan published that the library now skips
				// (an extra, a sample) goes missing like a removed one.
				removed = append(slices.Clone(removed), excluded...)
			} else {
				published, err = c.scanner.PublishStorageEbooksTx(ctx, tx, folder, location, source.ConfigurationRevision, books)
			}
			if err != nil {
				return err
			}
			removedFiles, err = c.scanner.MarkStorageEbooksRemovedTx(ctx, tx, location, removed)
			return err
		}
		if err = c.sources.ApplyPageAuthorizedSkipping(job, lease, checkpoint, page, skipDirectory, authorize, publish); err != nil {
			return result, err
		}
		matches = slices.DeleteFunc(matches, func(m scanner.StorageMovieMatch) bool {
			attempted := matchAttempted[m.ContentID]
			matchAttempted[m.ContentID] = true
			return attempted
		})
		if len(matches) > 0 && c.movieMetadata != nil {
			matching.Add(1)
			go func() {
				defer matching.Done()
				c.matchMovies(job, folder.ID, matches, matchStats)
			}()
		}
		result.ScanResult.New += published.New
		result.ScanResult.Updated += published.Updated
		result.ScanResult.Unchanged += published.Unchanged + skipped
		result.ScanResult.Missing += removedFiles
		listed += len(page.GetEntries())
		reportProgress(job, ProgressUpdate{Phase: "processing", Message: "Listing storage source",
			FilesDiscovered: listed, FilesProcessed: listed, New: result.ScanResult.New, Updated: result.ScanResult.Updated})
	}
}

// releaseLease runs after the scan has stopped, possibly because ctx was
// canceled, so it uses a bounded context of its own. A failed release only
// delays a retry until the claim expires.
func (c *StorageScanner) releaseLease(ctx context.Context, lease storagesource.Lease) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := c.sources.Release(releaseCtx, lease); err != nil {
		slog.WarnContext(ctx, "storage scan: release lease", "component", "libraryingest", "storage_source", lease.SourceKey, "run_id", lease.RunID, "error", err)
	}
}

// snapshot loads the source's approved, enabled installation.
func (c *StorageScanner) snapshot(ctx context.Context, source storagesource.SourceConfig) (*plugins.NativeStorageSnapshot, error) {
	snapshot, err := c.host.Registry.Snapshot(ctx, source.Key, source.OwnerID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || source.InstallationID == nil ||
		int64(snapshot.Installation.ID) != *source.InstallationID || !snapshot.Installation.Enabled || snapshot.Generation == 0 ||
		!nativestorage.SameSource(source, snapshot.Source) || !snapshot.Source.Enabled {
		return nil, storagesource.ErrSourceUnavailable
	}
	return snapshot, nil
}

// prepare turns a page's EPUB and PDF entries into publishable books. Entries
// with provider metadata need no file access; others are read and parsed.
// Other files are recorded in the listing journal but not published.
func (c *StorageScanner) prepare(ctx context.Context, media mediasource.Source, source storagesource.SourceConfig, entries []*storagev1.Entry) ([]scanner.StorageEbook, int, error) {
	books := make([]scanner.StorageEbook, 0, len(entries))
	skipped := 0
	for _, e := range entries {
		if e.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
			continue
		}
		if suffix := strings.ToLower(path.Ext(e.GetName())); suffix != ".epub" && suffix != ".pdf" {
			skipped++
			continue
		}
		if book, ok := scanner.StorageEbookFromEntry(e); ok {
			books = append(books, book)
			continue
		}
		file, err := mediasource.Open(ctx, media, mediasource.Ref{SourceID: source.ProviderSourceID, EntryID: e.GetId(), Revision: e.GetRevision()})
		if err != nil {
			return nil, 0, fmt.Errorf("open storage ebook %s: %w", e.GetId(), err)
		}
		book, parseErr := scanner.ParseStorageEbook(ctx, e, file)
		if err := errors.Join(parseErr, file.Close()); err != nil {
			return nil, 0, fmt.Errorf("parse storage ebook %s: %w", e.GetId(), err)
		}
		books = append(books, book)
	}
	return books, skipped, nil
}

// describedSource checks the configured source and retained read policy.
// Tests without a retained runtime snapshot keep strict admission.
func describedSource(d *storagev1.DescribeResponse, source storagesource.SourceConfig, snapshots ...*plugins.NativeStorageSnapshot) error {
	var snapshot *plugins.NativeStorageSnapshot
	if len(snapshots) == 1 {
		snapshot = snapshots[0]
	}
	if err := nativestorage.ValidateReadDescription(d, source.ProviderSourceID, source.RootEntryID, snapshot); err != nil {
		return storagesource.ErrSourceUnavailable
	}
	return nil
}

var errStorageRenewalStopped = errors.New("storage renewal deliberately stopped")

// SQL may wrap cancellation. Only discard an error tree whose leaves are all
// context.Canceled; a joined deadline or genuine fence must survive Stop.
func storageRenewalOnlyCanceled(err error) bool {
	if err == context.Canceled { //nolint:errorlint // Require exact cancellation leaves; errors.Is could hide a joined deadline/fence or a custom Is leaf.
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !storageRenewalOnlyCanceled(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return storageRenewalOnlyCanceled(wrapped.Unwrap())
	}
	return false
}

// startStorageRenewal covers long parsing/cover I/O. Stop cancels any outstanding
// SQL call and joins the worker before its caller releases runtime admission.
func startStorageRenewal(ctx context.Context, cancel context.CancelCauseFunc, interval time.Duration, renew func(context.Context) error) func() {
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
				if cause != errStorageRenewalStopped || !storageRenewalOnlyCanceled(err) { //nolint:errorlint // Only this private Stop cause permits suppression; wrapped or equivalent causes must propagate.
					cancel(fmt.Errorf("storage scan renewal: %w", err))
				}
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { stop(errStorageRenewalStopped); <-done }) }
}
