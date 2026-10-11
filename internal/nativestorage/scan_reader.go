package nativestorage

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ScanReader opens pinned entries for a host scan. It grants no viewer access
// and accepts no path, URL or independently selected entry identity.
// Its lifetime must remain inside the scan's publication boundary.
type ScanReader struct {
	pool             *pgxpool.Pool
	host             *Host
	runtime          Runtime
	nativeMovieReady bool
}

// ScanPage seals the validated provider page, its current pending checkpoint
// and authority snapshot. Only its originating reader may use it. It remains
// usable only while that exact lease/checkpoint/source authority is current.
type ScanPage struct {
	identity   *ScanPage
	reader     *ScanReader
	location   storagesource.Location
	source     storagesource.SourceConfig
	lease      storagesource.Lease
	checkpoint storagesource.Checkpoint
	page       *storagev1.ListResponse
	snapshot   *plugins.NativeStorageSnapshot
	owners     scanOwnerEvidence
}
type scanOwnerEvidence struct {
	folderOwner                    uuid.UUID
	folderRevision, sourceRevision int64
}

func NewScanReader(pool *pgxpool.Pool, host *Host) (*ScanReader, error) {
	if pool == nil || host == nil || host.Registry == nil || host.Manager == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	return &ScanReader{pool: pool, host: host, runtime: ManagedRuntime{Manager: host.Manager}}, nil
}

// SetNativeMovieReady is startup-only, before this reader admits any pages or
// entries. It must never race provider reads or authority checks.
func (r *ScanReader) SetNativeMovieReady(ready bool) {
	if r != nil {
		r.nativeMovieReady = ready
	}
}

// AdmitScanPage accepts a page fetched by the trusted scan coordinator for its
// current checkpoint. It clones before validating so later caller mutations
// cannot redirect an open or alter the metadata accepted from pinned Stat.
func (r *ScanReader) AdmitScanPage(ctx context.Context, location storagesource.Location, source storagesource.SourceConfig, lease storagesource.Lease, checkpoint storagesource.Checkpoint, page *storagev1.ListResponse) (*ScanPage, error) {
	if ctx == nil || r == nil || r.pool == nil || r.host == nil || r.host.Registry == nil || r.runtime == nil ||
		lease.RunID == uuid.Nil || lease.SourceKey != source.Key || lease.ConfigurationRevision != source.ConfigurationRevision || lease.Epoch <= 0 || lease.Owner == "" || checkpoint.Complete {
		return nil, storagesource.ErrSourceUnavailable
	}
	copyPage := proto.CloneOf(page)
	if err := mediasource.ValidatePage(checkpoint.Cursor, copyPage); err != nil {
		return nil, err
	}
	if source.InstallationID != nil {
		id := *source.InstallationID
		source.InstallationID = &id
	}
	snapshot, err := r.host.Registry.Snapshot(ctx, source.Key, source.OwnerID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Installation == nil || !SameSource(source, snapshot.Source) {
		return nil, storagesource.ErrSourceUnavailable
	}
	// Snapshot is registry-owned output; clone the protobuf/config and addressing
	// data too, so the page retains no caller- or runtime-owned mutable objects.
	frozen := *snapshot
	installation := *snapshot.Installation
	frozen.Installation = &installation
	frozen.Source = source
	frozen.Manifest = proto.CloneOf(snapshot.Manifest)
	frozen.Config = proto.CloneOf(&publicv1.ConfigureRequest{Config: snapshot.Config}).Config
	p := &ScanPage{reader: r, location: location, source: source, lease: lease, checkpoint: checkpoint, page: copyPage, snapshot: &frozen}
	p.identity = p
	p.owners, err = r.authorize(ctx, p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func sameScanSnapshot(a, b *plugins.NativeStorageSnapshot) bool {
	return a != nil && b != nil && a.Installation != nil && b.Installation != nil && SameSource(a.Source, b.Source) &&
		a.Generation == b.Generation && a.ArtifactChecksum == b.ArtifactChecksum &&
		a.Installation.ID == b.Installation.ID && a.Installation.PluginID == b.Installation.PluginID &&
		a.Installation.Version == b.Installation.Version && a.Installation.InstallPath == b.Installation.InstallPath &&
		a.Installation.Enabled && b.Installation.Enabled &&
		proto.Equal(a.Manifest, b.Manifest) && proto.Equal(&publicv1.ConfigureRequest{Config: a.Config}, &publicv1.ConfigureRequest{Config: b.Config})
}

// authorize checks approved runtime bytes/config first, then retains authority
// in the existing installation -> source -> run lock order. No DB connection
// or transaction survives this call, and none spans provider I/O.
func (r *ScanReader) authorize(ctx context.Context, p *ScanPage) (scanOwnerEvidence, error) {
	var owners scanOwnerEvidence
	if p == nil || p.identity != p || p.reader != r {
		return owners, storagesource.ErrReferenceConflict
	}
	snapshot, err := r.host.Registry.Snapshot(ctx, p.source.Key, p.source.OwnerID)
	if err != nil {
		return owners, err
	}
	if !sameScanSnapshot(p.snapshot, snapshot) {
		return owners, storagesource.ErrSourceUnavailable
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return owners, err
	}
	defer nativeDomainRollback(ctx, tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return owners, err
	}
	resources := resourcetenancy.NewStore(r.pool)
	resources.SetNativeMovieReady(r.nativeMovieReady)
	if err = resources.RequireStorageScanTx(ctx, tx, p.location, p.source); err != nil {
		return owners, err
	}
	// A snapshot read before acquiring authority locks cannot fence a generation
	// or archive change that completed while those locks were being acquired.
	var generation int64
	var path, checksum string
	var manifestJSON []byte
	if err = tx.QueryRow(ctx, `SELECT i.runtime_generation,i.install_path,a.checksum,a.manifest_json FROM plugin_installations i JOIN plugin_archives a ON a.plugin_installation_id=i.id WHERE i.id=$1`, *p.source.InstallationID).Scan(&generation, &path, &checksum, &manifestJSON); err != nil {
		return owners, err
	}
	var manifest publicv1.PluginManifest
	if err = protojson.Unmarshal(manifestJSON, &manifest); err != nil {
		return owners, err
	}
	if generation <= 0 || uint64(generation) != p.snapshot.Generation || path != p.snapshot.Installation.InstallPath || checksum != p.snapshot.ArtifactChecksum || !proto.Equal(&manifest, p.snapshot.Manifest) {
		return owners, storagesource.ErrSourceUnavailable
	}
	if err = tx.QueryRow(ctx, `SELECT f.owner_id,fo.revision,so.revision FROM media_folders f JOIN resource_owners fo ON fo.id=f.owner_id JOIN resource_owners so ON so.id=$2 WHERE f.id=$1`, p.location.FolderID, p.source.OwnerID).Scan(&owners.folderOwner, &owners.folderRevision, &owners.sourceRevision); err != nil {
		return owners, err
	}
	if p.owners.folderOwner != uuid.Nil && p.owners != owners {
		return owners, storagesource.ErrSourceUnavailable
	}
	var discoveryRun uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT discovery_run_id FROM bloem_storage_sources WHERE key=$1`, p.source.Key).Scan(&discoveryRun); err != nil {
		return owners, err
	}
	if discoveryRun != p.lease.RunID {
		return owners, storagesource.ErrStaleLease
	}
	if err = storagesource.ValidatePendingPageTx(ctx, tx, p.lease, p.checkpoint); err != nil {
		return owners, err
	}
	return owners, tx.Commit(ctx)
}

// OpenScanEntry selects only by index inside the sealed provider page. A page
// entry is not a catalog file or a persisted grant. Stat must prove all pinned
// page facts before this file can supply bytes to a trusted probe.
func (r *ScanReader) OpenScanEntry(ctx context.Context, p *ScanPage, index int) (*ScanEntryFile, error) {
	if ctx == nil || r == nil || p == nil || p.identity != p || p.reader != r || index < 0 || index >= len(p.page.GetEntries()) {
		return nil, storagesource.ErrReferenceConflict
	}
	entry := p.page.Entries[index]
	if entry.GetKind() != storagev1.EntryKind_ENTRY_KIND_FILE {
		return nil, storagesource.ErrReferenceConflict
	}
	release, err := r.host.AcquireOpen()
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if _, err = r.authorize(ctx, p); err != nil {
		return nil, err
	}
	snapshot := p.snapshot
	borrowed, err := r.runtime.Borrow(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: proto.CloneOf(snapshot.Manifest), Config: proto.CloneOf(&publicv1.ConfigureRequest{Config: snapshot.Config}).Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return nil, err
	}
	if err = ValidateReadDescription(borrowed.Description, p.source.ProviderSourceID, p.source.RootEntryID, snapshot); err != nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	if borrowed.Context == nil || borrowed.Source == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	readCtx, cancel := context.WithCancelCause(ctx)
	state := &scanFileState{ctx: readCtx, cancel: cancel, reader: r, page: p, runtime: borrowed.Context, release: release}
	state.stop = context.AfterFunc(borrowed.Context, func() { cancel(storageplugin.ErrUnavailable) })
	defer func() {
		if !transferred {
			state.stop()
			cancel(context.Canceled)
		}
	}()
	ref := mediasource.Ref{SourceID: p.source.ProviderSourceID, EntryID: entry.Id, Revision: entry.Revision}
	source := &scanEntrySource{raw: borrowed.Source, state: state, entry: entry, ref: ref}
	opened, err := mediasource.Open(readCtx, source, ref)
	if err != nil {
		return nil, err
	}
	transferred = true
	return &ScanEntryFile{file: opened, state: state}, nil
}

// ScanEntryFile owns host admission through Close and joins every active read.
// Cancel stops I/O but does not release that ownership; ToolInput must close
// only after its child and handlers join. Err is sticky across all source or
// authority failures, even when earlier pinned ranges succeeded.
type ScanEntryFile struct {
	file     mediasource.File
	state    *scanFileState
	once     sync.Once
	closeErr error
}

var _ mediasource.File = (*ScanEntryFile)(nil)

type scanFileState struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	reader  *ScanReader
	page    *ScanPage
	runtime context.Context
	stop    func() bool
	release func()
	mu      sync.Mutex
	closed  bool
	sticky  error
	active  sync.WaitGroup
}

func (s *scanFileState) fail(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	if s.sticky == nil {
		s.sticky = err
	}
	sticky := s.sticky
	s.mu.Unlock()
	s.cancel(sticky)
	return sticky
}
func (s *scanFileState) enter() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return os.ErrClosed
	}
	if s.sticky != nil {
		err := s.sticky
		s.mu.Unlock()
		return err
	}
	s.active.Add(1)
	s.mu.Unlock()
	return nil
}
func (s *scanFileState) check(ctx context.Context) error {
	s.mu.Lock()
	sticky := s.sticky
	s.mu.Unlock()
	if sticky != nil {
		return sticky
	}
	if err := s.ctx.Err(); err != nil {
		return s.fail(context.Cause(s.ctx))
	}
	if err := s.runtime.Err(); err != nil {
		return s.fail(storageplugin.ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return s.fail(context.Cause(ctx))
	}
	_, err := s.reader.authorize(ctx, s.page)
	if err == nil {
		err = s.ctx.Err()
	}
	if err == nil && s.runtime.Err() != nil {
		err = storageplugin.ErrUnavailable
	}
	return s.fail(err)
}
func (f *ScanEntryFile) Info() mediasource.Info { return f.file.Info() }
func (f *ScanEntryFile) Err() error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return f.state.sticky
}
func (f *ScanEntryFile) Context() context.Context { return f.state.ctx }
func (f *ScanEntryFile) Cancel()                  { f.state.cancel(context.Canceled) }
func (f *ScanEntryFile) Reauthorize(ctx context.Context) error {
	if ctx == nil {
		return storagesource.ErrSourceUnavailable
	}
	if err := f.state.enter(); err != nil {
		return err
	}
	defer f.state.active.Done()
	return f.state.check(ctx)
}
func (f *ScanEntryFile) ReadAt(data []byte, offset int64) (int, error) {
	if err := f.state.enter(); err != nil {
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		return 0, err
	}
	n, err := f.file.ReadAt(data, offset)
	return f.finishRead(data, n, err)
}
func (f *ScanEntryFile) Read(data []byte) (int, error) {
	if err := f.state.enter(); err != nil {
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		return 0, err
	}
	n, err := f.file.Read(data)
	return f.finishRead(data, n, err)
}
func (f *ScanEntryFile) finishRead(data []byte, n int, err error) (int, error) {
	// EOF is normal only when the file synthesized it at its known size.
	// A range's terminal EOF is sticky; earlier ranges from that same call
	// must not survive as an apparently successful partial read.
	if sticky := f.Err(); sticky != nil {
		clear(data)
		return 0, sticky
	}
	if err != nil && !errors.Is(err, io.EOF) {
		clear(data)
		return 0, f.state.fail(err)
	}
	return n, err
}
func (f *ScanEntryFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.state.enter(); err != nil {
		return 0, err
	}
	defer f.state.active.Done()
	if err := f.state.check(f.state.ctx); err != nil {
		return 0, err
	}
	return f.file.Seek(offset, whence)
}
func (f *ScanEntryFile) Close() error {
	f.once.Do(func() {
		f.state.mu.Lock()
		f.state.closed = true
		f.state.mu.Unlock()
		f.state.cancel(context.Canceled)
		f.state.stop()
		f.state.active.Wait()
		f.closeErr = f.file.Close()
		f.state.release()
	})
	return f.closeErr
}

type scanEntrySource struct {
	raw   mediasource.Source
	state *scanFileState
	entry *storagev1.Entry
	ref   mediasource.Ref
}

func (s *scanEntrySource) Stat(ctx context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	if ref != s.ref {
		return mediasource.Info{}, s.state.fail(storagesource.ErrReferenceConflict)
	}
	if err := s.state.check(ctx); err != nil {
		return mediasource.Info{}, err
	}
	info, err := s.raw.Stat(ctx, ref)
	after := s.state.check(ctx)
	if err = errors.Join(err, after); err != nil {
		return mediasource.Info{}, s.state.fail(err)
	}
	entry := s.entry
	if info.Name != entry.Name || info.LogicalPath != entry.LogicalPath || info.Size != entry.Size || info.Revision != entry.Revision || !info.ModifiedAt.Equal(time.Unix(0, entry.ModifiedUnixNano)) {
		return mediasource.Info{}, s.state.fail(storagesource.ErrReferenceConflict)
	}
	return info, nil
}
func (s *scanEntrySource) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	if ref != s.ref {
		return s.state.fail(storagesource.ErrReferenceConflict)
	}
	if err := s.state.check(ctx); err != nil {
		return err
	}
	err := s.raw.ReadRange(ctx, ref, offset, length, w)
	after := s.state.check(ctx)
	return s.state.fail(errors.Join(err, after))
}
