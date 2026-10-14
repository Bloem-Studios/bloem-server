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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
)

// StorageIngestor scans libraries whose location is a storage source.
type StorageIngestor interface {
	LocationSelections(context.Context, int) ([]storagesource.LocationSelection, error)
	ScanStorageFolder(context.Context, *models.MediaFolder, storagesource.LocationSelection) (*Result, error)
}

// SetStorageIngestor is startup-only, before scans are admitted.
func (e *Executor) SetStorageIngestor(storage StorageIngestor) {
	if e != nil {
		e.storageIngestor = storage
	}
}

// MixedLocationIngestionReady witnesses the composed filesystem and native
// lanes at startup. Media-specific playback admission remains a separate gate.
func (e *Executor) MixedLocationIngestionReady() bool {
	if e == nil || e.scanner == nil || e.matcher == nil {
		return false
	}
	storage, ok := e.storageIngestor.(*StorageScanner)
	return ok && storage != nil && storage.host != nil && storage.host.Registry != nil && storage.host.Manager != nil && storage.sources != nil && storage.resources != nil && storage.scanner.NativeStorageScanConfigured()
}

// tryStorageIngest scans a library with a storage location and reports whether
// it did. Libraries without one continue with the filesystem scanner.
func (e *Executor) tryStorageIngest(ctx context.Context, folder *models.MediaFolder, mode scopeMode) (*Result, bool, error) {
	if e == nil || folder == nil || e.storageIngestor == nil || mode != scopeModeLibrary {
		return nil, false, nil
	}
	selections, err := e.storageIngestor.LocationSelections(ctx, folder.ID)
	if err != nil {
		return nil, true, err
	}
	if len(selections) == 0 {
		return nil, false, nil
	}
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := e.storageIngest(scanCtx, cancel, folder, selections)
	return result, true, storageScanOutcome(scanCtx, folder, selections[0].Location, err)
}

func (e *Executor) storageIngest(scanCtx context.Context, cancel context.CancelFunc, folder *models.MediaFolder, selections []storagesource.LocationSelection) (*Result, error) {
	mode := scopeModeLibrary
	claim := scopeClaim{folderID: folder.ID, mode: scopeModeLibrary}
	entry, err := e.begin(scanCtx, claim, cancel)
	if err != nil {
		return nil, err
	}
	defer e.finish(entry)
	result := &Result{ScanResult: &scanner.ScanResult{}}
	var failures []error
	if len(folder.Paths) > 0 {
		if e.scanner == nil || e.matcher == nil {
			return nil, fmt.Errorf("library ingest executor is not fully configured")
		}
		local, localErr := e.executeFilesystem(scanCtx, folder, mode, claim, false)
		mergeIngestResult(result, local)
		if localErr != nil && (local == nil || local.ScanResult == nil || local.ScanResult.Errors == 0) {
			result.ScanResult.Errors++
		}
		if localErr != nil {
			failures = append(failures, localErr)
		}
	}
	for _, selection := range selections {
		if scanCtx.Err() != nil {
			failures = append(failures, scanCtx.Err())
			break
		}
		lane, laneErr := e.storageIngestor.ScanStorageFolder(scanCtx, folder, selection)
		mergeIngestResult(result, lane)
		if laneErr != nil && (lane == nil || lane.ScanResult == nil || lane.ScanResult.Errors == 0) {
			result.ScanResult.Errors++
		}
		if laneErr != nil {
			failures = append(failures, fmt.Errorf("storage location %s: %w", selection.Location.ID, laneErr))
		}
	}
	if len(failures) == 0 && e.folders != nil {
		if err := e.folders.UpdateLastScanned(scanCtx, folder.ID, e.now().UTC()); err != nil {
			failures = append(failures, err)
		}
	}
	if shouldPublish(result) && e.events != nil {
		if err := e.events.Publish(scanCtx, cache.ChannelCatalog, cache.Event{Type: cache.EventScanComplete, Payload: strconv.Itoa(folder.ID)}); err != nil {
			failures = append(failures, err)
		}
	}
	if shouldPublish(result) && e.realtime != nil {
		if err := e.realtime.PublishCatalogLibraryChanged(scanCtx, notifications.LibraryChangeEvent{LibraryID: folder.ID, Reason: "scan", New: result.ScanResult.New, Updated: result.ScanResult.Updated, Missing: result.ScanResult.Missing, MatchedFiles: result.MatchedFiles, RetriedItems: result.RetriedItems, StillUnmatchedWarnings: result.StillUnmatchedWarnings}); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		reportProgress(scanCtx, ProgressUpdate{Phase: "completed", Message: "Scan completed", New: result.ScanResult.New, Updated: result.ScanResult.Updated, Unchanged: result.ScanResult.Unchanged, Errors: result.ScanResult.Errors, MatchedFiles: result.MatchedFiles, RetriedItems: result.RetriedItems})
	}
	return result, errors.Join(failures...)
}

func mergeIngestResult(dst, src *Result) {
	if src == nil {
		return
	}
	dst.MatchedFiles += src.MatchedFiles
	dst.RetriedItems += src.RetriedItems
	dst.StillUnmatchedWarnings += src.StillUnmatchedWarnings
	dst.ScanDuration += src.ScanDuration
	dst.MatchDuration += src.MatchDuration
	dst.RetryDuration += src.RetryDuration
	if v := src.ScanResult; v != nil {
		d := dst.ScanResult
		d.New += v.New
		d.Updated += v.Updated
		d.Unchanged += v.Unchanged
		d.Missing += v.Missing
		d.Errors += v.Errors
		d.FilesDeleted += v.FilesDeleted
		d.MembershipsRemoved += v.MembershipsRemoved
		d.ItemsDeleted += v.ItemsDeleted
		d.MissingSkippedProtected += v.MissingSkippedProtected
		d.EmptyRootGuarded = d.EmptyRootGuarded || v.EmptyRootGuarded
		d.UnreachableRoots = append(d.UnreachableRoots, v.UnreachableRoots...)
		d.SuspectEmptyRoots = append(d.SuspectEmptyRoots, v.SuspectEmptyRoots...)
		d.RootObservations = append(d.RootObservations, v.RootObservations...)
	}
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
	nativeAudioReady bool
	audioGate        *atomic.Bool
	audioReader      *nativestorage.ScanReader
	audioProbeConfig scanner.NativeProbeConfig
	movieReader      *nativestorage.ScanReader
	movieProbeConfig scanner.NativeProbeConfig
	movieMetadata    NativeMovieMetadata
}

var _ StorageIngestor = (*StorageScanner)(nil)

func NewStorageScanner(host *nativestorage.Host, sources *storagesource.Repository, resources *resourcetenancy.Store, publisher *scanner.Scanner) (*StorageScanner, error) {
	if host == nil || host.Registry == nil || host.Manager == nil || sources == nil || resources == nil || publisher == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	publisher.SetMaintenanceFileScope(scanner.NativeMaintenanceFileScope{})
	return &StorageScanner{host: host, sources: sources, resources: resources, scanner: publisher, leaseTTL: time.Minute}, nil
}

func (c *StorageScanner) LocationSelections(ctx context.Context, folderID int) ([]storagesource.LocationSelection, error) {
	return c.sources.LocationSelections(ctx, folderID)
}

func (c *StorageScanner) ScanStorageFolder(ctx context.Context, folder *models.MediaFolder, selection storagesource.LocationSelection) (result *Result, err error) {
	location := selection.Location
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
	if !librarykind.SupportsNativeStorageKinds(folder.Type, c.nativeMovieReady, c.nativeAudioReady && c.audioGate != nil && c.audioGate.Load()) || !folder.Enabled {
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
	pinned, err := c.resources.StorageScanResources(ctx, location, source)
	if err != nil {
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
		err = normalizeNativeScanError(ctx, job, err)
	}()
	provider := session.Provider()
	description, err := storageplugin.Describe(job, provider)
	if err != nil {
		return result, err
	}
	if err = describedSource(description, source, snapshot); err != nil {
		return result, err
	}
	if librarykind.IsAudiobook(folder.Type) && !declaresNativeAudio(description, source.ProviderSourceID) {
		return result, storagesource.ErrSourceUnavailable
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
		return c.resources.RequireStorageScanTx(ctx, tx, location, source, pinned)
	}
	lease, err := c.beginLocation(job, location, "storage-scan:"+uuid.NewString(), authorize)
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
	var confirmLease func()
	stopRenew, confirmLease = startStorageRenewal(job, cancel, c.leaseTTL/3, c.leaseTTL, func(ctx context.Context) error {
		return c.sources.RenewAuthorized(ctx, lease, c.leaseTTL, authorize)
	})
	defer stopRenew()

	media := mediasource.NewPluginSource(provider)
	skipDirectory := func(entry *storagev1.Entry) bool { return scanner.StorageDirectorySkipped(folder.Type, entry) }
	listed := 0
	for {
		if err = nativeScanCause(ctx, job); err != nil {
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
			var swept scanner.ScanResult
			var audioFinal scanner.StorageAudioResult
			var audioHidden int
			var orphanedImageDirs []string
			finalize := func(ctx context.Context, tx pgx.Tx) error {
				var err error
				if librarykind.IsAudiobook(folder.Type) {
					audioFinal, audioHidden, err = c.scanner.FinalizeStorageAudioLocationTx(ctx, tx, folder, location, lease)
					if err != nil {
						return err
					}
				}
				swept, orphanedImageDirs, err = c.scanner.SweepStorageLocationCountsTx(ctx, tx, folder, location)
				return err
			}
			missing, err := c.sources.CompleteLocationAuthorized(job, lease, authorize, finalize)
			if err != nil {
				return result, err
			}
			result.ScanResult.Missing += missing + audioHidden
			result.ScanResult.New += audioFinal.New
			result.ScanResult.Updated += audioFinal.Updated
			result.ScanResult.Unchanged += audioFinal.Unchanged
			result.ScanResult.Errors += audioFinal.BooksRejected
			result.ScanResult.FilesDeleted += swept.FilesDeleted
			result.ScanResult.MembershipsRemoved += swept.MembershipsRemoved
			result.ScanResult.ItemsDeleted += swept.ItemsDeleted
			c.scanner.CleanupStorageLibraryArtwork(job, orphanedImageDirs)
			matchStats.log(job, folder.ID)
			return result, nil
		}
		page, err := c.sources.FetchPage(job, lease, checkpoint, provider)
		if errors.Is(err, storagesource.ErrCheckpointRestart) {
			continue
		}
		if err != nil {
			return result, err
		}
		var books []scanner.StorageEbook
		var movies []scanner.StorageMovie
		var audio []scanner.StorageAudioTrack
		var skipped int
		var excluded []string
		if librarykind.IsMovie(folder.Type) {
			movies, skipped, excluded, err = c.prepareMovies(job, location, source, lease, checkpoint, page)
		} else if librarykind.IsAudiobook(folder.Type) {
			audio, skipped, err = c.prepareAudio(job, location, source, lease, checkpoint, page)
			page.ChangeToken = ""
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
		var audioRejected int
		var matches []scanner.StorageMovieMatch
		publish := func(ctx context.Context, tx pgx.Tx, files []*storagev1.Entry, _ []string) error {
			allowed := make(map[string]bool, len(files))
			for _, file := range files {
				allowed[file.Id] = true
			}
			uniqueBooks := make([]scanner.StorageEbook, 0, len(books))
			for _, book := range books {
				if allowed[book.Entry.Id] {
					uniqueBooks = append(uniqueBooks, book)
				}
			}
			uniqueMovies := make([]scanner.StorageMovie, 0, len(movies))
			for _, movie := range movies {
				if allowed[movie.Entry.Id] {
					uniqueMovies = append(uniqueMovies, movie)
				}
			}
			var err error
			if librarykind.IsMovie(folder.Type) {
				if len(uniqueMovies) > 0 {
					var moviePage scanner.StorageMoviePublishResult
					moviePage, err = c.scanner.PublishStorageMoviesTx(ctx, tx, folder, location, source.ConfigurationRevision, uniqueMovies)
					published, matches = moviePage.StoragePublishResult, moviePage.Matches
				}
			} else if librarykind.IsAudiobook(folder.Type) {
				var allowedTracks []scanner.StorageAudioTrack
				for _, track := range audio {
					if allowed[track.Entry.Id] {
						allowedTracks = append(allowedTracks, track)
					}
				}
				var audioPage scanner.StorageAudioResult
				audioPage, err = c.scanner.PublishStorageAudioPageTx(ctx, tx, scanner.StorageAudioPage{Folder: folder, Location: location, Lease: lease, ConfigurationRevision: source.ConfigurationRevision, DirectoryID: checkpoint.DirectoryID, Complete: page.GetComplete(), Tracks: allowedTracks})
				published, audioRejected = audioPage.StoragePublishResult, audioPage.BooksRejected
			} else {
				if len(uniqueBooks) == 0 {
					return nil
				}
				published, err = c.scanner.PublishStorageEbooksTx(ctx, tx, folder, location, source.ConfigurationRevision, uniqueBooks)
			}
			if err != nil {
				return err
			}
			// Deterministically excluded movie entries cannot retain publication edges.
			if len(excluded) > 0 {
				if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges WHERE location_id=$1 AND entry_id=ANY($2::text[])`, location.ID, excluded); err != nil {
					return err
				}
			}
			// Explicit removals change retained directory edges in this page.
			// Missing flags are committed only by successful location completion.
			return nil
		}
		if err = c.sources.ApplyPageAuthorizedSkipping(job, lease, checkpoint, page, skipDirectory, authorize, publish); err != nil {
			return result, err
		}
		// The committed page renewed the lease.
		confirmLease()
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
		result.ScanResult.Errors += audioRejected
		result.ScanResult.New += published.New
		result.ScanResult.Updated += published.Updated
		result.ScanResult.Unchanged += published.Unchanged + skipped
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

// storageRenewalAttemptLimit bounds one renewal transaction.
const storageRenewalAttemptLimit = 10 * time.Second

// startStorageRenewal covers long parsing/cover I/O. Stop cancels any outstanding
// SQL call and joins the worker before its caller releases runtime admission.
// Confirm records that the lease was renewed some other way, as each committed
// page renews it.
//
// A renewal waits for the library lock a page transaction holds. An attempt
// that runs out of time while the lease was confirmed within ttl is retried
// rather than failing the scan: the database still fences every page and
// completion, so a lease that did lapse fails the scan there or on a later
// attempt. Any other failure, or a timeout once ttl has passed without a
// confirmation, stops the scan.
func startStorageRenewal(ctx context.Context, cancel context.CancelCauseFunc, interval, ttl time.Duration, renew func(context.Context) error) (stop, confirm func()) {
	renewalCtx, stopRenewal := context.WithCancelCause(ctx)
	var confirmed atomic.Int64
	confirm = func() { confirmed.Store(time.Now().UnixNano()) }
	confirm()
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
			bounded, release := context.WithTimeout(renewalCtx, min(storageRenewalAttemptLimit, interval))
			err := renew(bounded)
			cause := context.Cause(bounded)
			release()
			if err == nil {
				confirm()
				continue
			}
			if storageRenewalAttemptTimedOut(renewalCtx, cause, err) && time.Since(time.Unix(0, confirmed.Load())) < ttl {
				slog.WarnContext(ctx, "storage scan renewal timed out; retrying while the lease is current", "component", "libraryingest", "error", err)
				continue
			}
			// Normal Stop only suppresses the cancellation it caused. A real
			// fence/provider/policy failure must never become full success just
			// because the caller concurrently finishes its last claim.
			if cause != errStorageRenewalStopped || !storageRenewalOnlyCanceled(err) { //nolint:errorlint // Only this private Stop cause permits suppression; wrapped or equivalent causes must propagate.
				cancel(fmt.Errorf("storage scan renewal: %w", err))
			}
			return
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { stopRenewal(errStorageRenewalStopped); <-done }) }, confirm
}

// storageRenewalAttemptTimedOut reports an attempt that ran out of its own
// time, while the scan continues, without a lease or authority fence.
func storageRenewalAttemptTimedOut(renewal context.Context, cause, err error) bool {
	return renewal.Err() == nil && errors.Is(cause, context.DeadlineExceeded) && errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, storagesource.ErrStaleLease) && !errors.Is(err, resourcetenancy.ErrResourceHidden) && !errors.Is(err, resourcetenancy.ErrAuthorizationStateChanged)
}

// A child stopped after an internal fence/process failure carries that failure,
// while actual parent cancellation keeps the queue's cancellation semantics.
func nativeScanCause(parent, job context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if cause := context.Cause(job); cause != nil {
		return cause
	}
	return job.Err()
}
func normalizeNativeScanError(parent, job context.Context, err error) error {
	cause := nativeScanCause(parent, job)
	if cause == nil {
		return err
	}
	if parent.Err() != nil {
		return errors.Join(err, cause)
	}
	if storageRenewalOnlyCanceled(cause) {
		return errors.Join(err, cause)
	}
	return errors.Join(withoutNativeCancellation(err), withoutNativeCancellation(cause))
}
func withoutNativeCancellation(err error) error {
	if err == nil || storageRenewalOnlyCanceled(err) {
		return nil
	}
	if !errors.Is(err, context.Canceled) {
		return err
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		out := make([]error, 0, len(joined.Unwrap()))
		for _, child := range joined.Unwrap() {
			if keep := withoutNativeCancellation(child); keep != nil {
				out = append(out, keep)
			}
		}
		return errors.Join(out...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return withoutNativeCancellation(wrapped.Unwrap())
	}
	return err
}

// Contention uses the existing scan's context and renews authority on every
// attempt. The fixed bound prevents an active foreign source from monopolizing
// a scan worker indefinitely; it is never absence evidence.
func (c *StorageScanner) beginLocation(ctx context.Context, location storagesource.Location, owner string, authorize storagesource.BeginAuthorizer) (storagesource.Lease, error) {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		lease, err := c.sources.BeginLocationAuthorized(wait, location, owner, c.leaseTTL, authorize)
		if !errors.Is(err, storagesource.ErrSourceBusy) {
			return lease, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lease, ctx.Err()
		case <-wait.Done():
			timer.Stop()
			return lease, storagesource.ErrSourceBusy
		case <-timer.C:
		}
	}
}
