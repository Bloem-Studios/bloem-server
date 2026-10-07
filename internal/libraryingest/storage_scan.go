package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"path"
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
	"google.golang.org/grpc"
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
	entry, err := e.begin(scanCtx, scopeClaim{folderID: folder.ID, mode: scopeModeLibrary}, cancel)
	if err != nil {
		return nil, true, err
	}
	defer e.finish(entry)
	reportProgress(scanCtx, ProgressUpdate{Phase: "preparing", Message: "Preparing storage scan"})
	result, err := e.storageIngestor.ScanStorageFolder(scanCtx, folder, location)
	if err != nil {
		return result, true, err
	}
	if e.folders != nil {
		if err = e.folders.UpdateLastScanned(scanCtx, folder.ID, e.now().UTC()); err != nil {
			return result, true, fmt.Errorf("update storage library last scanned: %w", err)
		}
	}
	if shouldPublish(result) && e.events != nil {
		if err = e.events.Publish(scanCtx, cache.ChannelCatalog, cache.Event{Type: cache.EventScanComplete, Payload: strconv.Itoa(folder.ID)}); err != nil {
			return result, true, err
		}
	}
	if shouldPublish(result) && e.realtime != nil {
		if err = e.realtime.PublishCatalogLibraryChanged(scanCtx, notifications.LibraryChangeEvent{
			LibraryID: folder.ID, Reason: "scan",
			New:     scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.New }),
			Updated: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Updated }),
			Missing: scanResultCount(result.ScanResult, func(v *scanner.ScanResult) int { return v.Missing }),
		}); err != nil {
			return result, true, err
		}
	}
	reportProgress(scanCtx, ProgressUpdate{Phase: "completed", Message: "Storage scan completed"})
	return result, true, nil
}

// StorageScanner lists a storage source and publishes each listed page to its
// library's catalog in the page's own transaction. Books with provider
// metadata publish without their files being read; others are parsed.
type StorageScanner struct {
	host      *nativestorage.Host
	sources   *storagesource.Repository
	resources *resourcetenancy.Store
	scanner   *scanner.Scanner
	leaseTTL  time.Duration
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
	if !librarykind.IsEbook(folder.Type) || !folder.Enabled {
		return result, fmt.Errorf("storage locations support enabled ebook libraries only")
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
	job, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopSession := context.AfterFunc(session.Context(), func() { cancel(storageplugin.ErrUnavailable) })
	defer stopSession()
	defer func() {
		if cause := context.Cause(job); cause != nil && !errors.Is(cause, context.Canceled) {
			err = errors.Join(err, cause)
		}
	}()
	provider := session.Provider()
	describeCtx, stopDescribe := context.WithTimeout(job, 10*time.Second)
	description, err := provider.Describe(describeCtx, &storagev1.DescribeRequest{}, grpc.MaxCallRecvMsgSize(1<<20))
	stopDescribe()
	if err != nil {
		return result, err
	}
	if err = describedSource(description, source); err != nil {
		return result, err
	}

	lease, err := c.sources.Begin(job, source.Key, "storage-scan:"+uuid.NewString(), c.leaseTTL)
	if err != nil {
		return result, err
	}
	if lease.ConfigurationRevision != source.ConfigurationRevision {
		return result, storagesource.ErrStaleLease
	}
	stopRenew := startStorageRenewal(job, cancel, c.leaseTTL/3, func(ctx context.Context) error {
		if err := c.resources.RequireStorageScan(ctx, location, source); err != nil {
			return err
		}
		return c.sources.Renew(ctx, lease, c.leaseTTL)
	})
	defer stopRenew()

	media := mediasource.NewPluginSource(provider)
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
			// Stop renewing first so a renewal cannot race completion.
			stopRenew()
			return result, c.sources.Complete(job, lease)
		}
		page, err := c.sources.FetchPage(job, lease, checkpoint, provider)
		if err != nil {
			return result, err
		}
		books, skipped, err := c.prepare(job, media, source, page.GetEntries())
		if err != nil {
			return result, err
		}
		var published scanner.StoragePublishResult
		publish := func(ctx context.Context, tx pgx.Tx, _ []*storagev1.Entry) error {
			var err error
			published, err = c.scanner.PublishStorageEbooksTx(ctx, tx, folder, location, source.ConfigurationRevision, books)
			return err
		}
		if err = c.sources.ApplyPage(job, lease, checkpoint, page, publish); err != nil {
			return result, err
		}
		result.ScanResult.New += published.New
		result.ScanResult.Updated += published.Updated
		result.ScanResult.Unchanged += published.Unchanged + skipped
		listed += len(page.GetEntries())
		reportProgress(job, ProgressUpdate{Phase: "processing", Message: "Listing storage source",
			FilesDiscovered: listed, FilesProcessed: listed, New: result.ScanResult.New, Updated: result.ScanResult.Updated})
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
		snapshot.Source.Key != source.Key || snapshot.Source.ConfigurationRevision != source.ConfigurationRevision || !snapshot.Source.Enabled {
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

// describedSource checks the provider still serves the configured source with
// revision-pinned reads.
func describedSource(d *storagev1.DescribeResponse, source storagesource.SourceConfig) error {
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
