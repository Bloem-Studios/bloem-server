//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

var errScanFixtureTerminal = errors.New("synthetic terminal pinned read failure")

type scanFixtureSource struct {
	info                 mediasource.Info
	ref                  mediasource.Ref
	data                 []byte
	stats, reads         atomic.Int32
	afterStat, afterRead func()
	terminal             error
	readStarted          chan struct{}
	readRelease          <-chan struct{}
}

func (s *scanFixtureSource) Stat(ctx context.Context, ref mediasource.Ref) (mediasource.Info, error) {
	s.stats.Add(1)
	if ref != s.ref {
		return mediasource.Info{}, storagesource.ErrReferenceConflict
	}
	if s.afterStat != nil {
		s.afterStat()
	}
	return s.info, ctx.Err()
}
func (s *scanFixtureSource) ReadRange(ctx context.Context, ref mediasource.Ref, offset, length int64, w io.Writer) error {
	s.reads.Add(1)
	if ref != s.ref {
		return storagesource.ErrReferenceConflict
	}
	if s.readStarted != nil {
		close(s.readStarted)
	}
	if s.readRelease != nil {
		<-s.readRelease
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data := s.data
	if data == nil {
		data = []byte("film")
	}
	_, err := w.Write(data[offset : offset+length])
	if s.afterRead != nil {
		s.afterRead()
	}
	return errors.Join(err, s.terminal)
}

type scanFixtureRuntime struct {
	lease       Lease
	afterBorrow func()
}

func (r *scanFixtureRuntime) Borrow(context.Context, storageplugin.Snapshot) (Lease, error) {
	if r.afterBorrow != nil {
		r.afterBorrow()
	}
	return r.lease, nil
}
func (*scanFixtureRuntime) Disable(int) {}

type scanReadFixture struct {
	r             *ScanReader
	location      storagesource.Location
	source        storagesource.SourceConfig
	lease         storagesource.Lease
	checkpoint    storagesource.Checkpoint
	page          *storagev1.ListResponse
	media         *scanFixtureSource
	runtime       *scanFixtureRuntime
	cancelRuntime context.CancelFunc
}

func newScanReadFixture(t *testing.T) *scanReadFixture {
	t.Helper()
	path, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	management, _, cmd := discoveryFixture(t, binary, "unused", true)
	var owner uuid.UUID
	if err := management.pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	snapshot, err := management.registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: cmd.ArtifactKey, Binary: binary, Config: cmd.Config, Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: cmd.ProviderSourceID, RootEntryID: installFixtureRoot, Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := createStorageLibrary(t, management.pool, "ebooks", snapshot.Source.Key)
	if err != nil {
		t.Fatal(err)
	}
	repo := storagesource.NewRepository(management.pool)
	location, ok, err := repo.FolderLocation(t.Context(), folder)
	if err != nil || !ok {
		t.Fatal("location", err)
	}
	lease, err := repo.Begin(t.Context(), snapshot.Source.Key, "scan-reader-fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, ok, err := repo.NextDirectory(t.Context(), lease)
	if err != nil || !ok {
		t.Fatal("checkpoint", err)
	}
	r, err := NewScanReader(management.pool, management.installDiscoveryHost)
	if err != nil {
		t.Fatal(err)
	}
	entry := &storagev1.Entry{Id: "opaque:film-id", Name: "Film.mkv", LogicalPath: "display-only/Film.mkv", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 4, ModifiedUnixNano: 123456789, Revision: "immutable-version-1"}
	media := &scanFixtureSource{ref: mediasource.Ref{SourceID: snapshot.Source.ProviderSourceID, EntryID: entry.Id, Revision: entry.Revision}, info: mediasource.Info{Name: entry.Name, LogicalPath: entry.LogicalPath, Size: 4, ModifiedAt: time.Unix(0, 123456789), Revision: entry.Revision}}
	runtimeCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	runtime := &scanFixtureRuntime{lease: Lease{Context: runtimeCtx, Source: media, Description: &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: snapshot.Source.ProviderSourceID, RootEntryId: snapshot.Source.RootEntryID, RevisionPinnedReads: true}}}}}
	r.runtime = runtime
	return &scanReadFixture{r: r, location: location, source: snapshot.Source, lease: lease, checkpoint: checkpoint, page: &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}, media: media, runtime: runtime, cancelRuntime: cancel}
}
func (f *scanReadFixture) admit(t *testing.T) *ScanPage {
	t.Helper()
	page, err := f.r.AdmitScanPage(t.Context(), f.location, f.source, f.lease, f.checkpoint, f.page)
	if err != nil {
		t.Fatal("admit validated scan page", err)
	}
	return page
}
func (f *scanReadFixture) mutate(t *testing.T, mode string) {
	t.Helper()
	var err error
	switch mode {
	case "lease":
		_, err = storagesource.NewRepository(f.r.pool).Begin(t.Context(), f.source.Key, f.lease.Owner, time.Minute)
	case "lease-expiry":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, f.lease.RunID)
	case "source":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, f.source.Key)
	case "root":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE bloem_storage_sources SET root_entry_id='changed-root' WHERE key=$1`, f.source.Key)
	case "generation":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1`, *f.source.InstallationID)
	case "configuration":
		_, err = f.r.host.Registry.ReplaceConfiguration(t.Context(), f.source.Key, f.source.OwnerID, map[string]map[string]any{"connection": {"source_id": f.source.ProviderSourceID}})
	case "owner":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE resource_owners SET revision=revision+1 WHERE id=$1`, f.source.OwnerID)
	case "folder":
		_, err = f.r.pool.Exec(t.Context(), `UPDATE media_folders SET enabled=false WHERE id=$1`, f.location.FolderID)
	case "checkpoint":
		err = storagesource.NewRepository(f.r.pool).ApplyPage(t.Context(), f.lease, f.checkpoint, f.page, nil)
	default:
		t.Fatal("unknown mutation", mode)
	}
	if err != nil {
		t.Fatal("mutate current scan authority", err)
	}
}
func TestScanEntryPinnedPageAndAdmission(t *testing.T) {
	f := newScanReadFixture(t)
	page := f.admit(t)
	// The sealed copy must survive mutation of caller-owned metadata and source.
	f.page.Entries[0].Id = "caller-mutated"
	f.page.Entries[0].Revision = "caller-mutated"
	*f.source.InstallationID = -1
	opened, err := f.r.OpenScanEntry(t.Context(), page, 0)
	if err != nil {
		t.Fatal("open sealed page entry", err)
	}
	defer opened.Close()
	data := make([]byte, 4)
	if n, err := opened.ReadAt(data, 0); n != 4 || err != nil || string(data) != "film" {
		t.Fatal("pinned read", n, string(data), err)
	}
	if opened.Info() != f.media.info || opened.Err() != nil {
		t.Fatal("immutable info or sticky error", opened.Info(), opened.Err())
	}
	if _, err := f.r.OpenScanEntry(t.Context(), page, 1); err == nil {
		t.Fatal("entry outside validated page admitted")
	}
	if _, err := f.r.OpenScanEntry(t.Context(), &ScanPage{}, 0); err == nil {
		t.Fatal("forged page admitted")
	}
	other := newScanReadFixture(t)
	if _, err := other.r.OpenScanEntry(t.Context(), page, 0); err == nil {
		t.Fatal("cross-reader page admitted")
	}
}
func TestScanEntryAuthorityBeforeAndAfterIO(t *testing.T) {
	for _, stage := range []string{"before-stat", "after-stat", "before-read", "after-read"} {
		t.Run(stage, func(t *testing.T) {
			for _, mutation := range []string{"lease", "lease-expiry", "source", "root", "generation", "configuration", "owner", "folder", "checkpoint"} {
				t.Run(mutation, func(t *testing.T) {
					f := newScanReadFixture(t)
					page := f.admit(t)
					if stage == "before-stat" {
						f.mutate(t, mutation)
					}
					if stage == "after-stat" {
						f.media.afterStat = func() { f.mutate(t, mutation) }
					}
					opened, err := f.r.OpenScanEntry(t.Context(), page, 0)
					if stage == "before-stat" || stage == "after-stat" {
						if opened != nil {
							opened.Close()
						}
						if err == nil {
							t.Fatal("revoked scan authority opened Stat")
						}
						if stage == "before-stat" && f.media.stats.Load() != 0 {
							t.Fatal("Stat ran after revocation")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					defer opened.Close()
					if stage == "before-read" {
						f.mutate(t, mutation)
					} else {
						f.media.afterRead = func() { f.mutate(t, mutation) }
					}
					data := make([]byte, 4)
					n, err := opened.ReadAt(data, 0)
					if n != 0 || err == nil || string(data) != "\x00\x00\x00\x00" {
						t.Fatal("revoked bytes committed", n, data, err)
					}
					if stage == "before-read" && f.media.reads.Load() != 0 {
						t.Fatal("Read ran after revocation")
					}
					sticky := opened.Err()
					if sticky == nil {
						t.Fatal("revocation was not sticky")
					}
					before := f.media.reads.Load()
					if _, err := opened.ReadAt(data, 0); !errors.Is(err, sticky) || f.media.reads.Load() != before {
						t.Fatal("terminal handle reused", err)
					}
				})
			}
		})
	}
}
func TestScanEntryStatFactsAndFreshDescribe(t *testing.T) {
	for _, mode := range []string{"name", "path", "size", "mtime", "revision", "unpinned", "root", "borrow-generation", "cross-location"} {
		t.Run(mode, func(t *testing.T) {
			f := newScanReadFixture(t)
			page := f.admit(t)
			switch mode {
			case "name":
				f.media.info.Name = "changed"
			case "path":
				f.media.info.LogicalPath = "changed"
			case "size":
				f.media.info.Size++
			case "mtime":
				f.media.info.ModifiedAt = f.media.info.ModifiedAt.Add(time.Nanosecond)
			case "revision":
				f.media.info.Revision = "other-version"
			case "unpinned":
				f.runtime.lease.Description.Sources[0].RevisionPinnedReads = false
			case "root":
				f.runtime.lease.Description.Sources[0].RootEntryId = "changed"
			case "borrow-generation":
				f.runtime.afterBorrow = func() { f.mutate(t, "generation") }
			case "cross-location":
				wrong := f.location
				wrong.FolderID++
				if _, err := f.r.AdmitScanPage(t.Context(), wrong, f.source, f.lease, f.checkpoint, f.page); err == nil {
					t.Fatal("cross-location admission")
				}
				return
			}
			opened, err := f.r.OpenScanEntry(t.Context(), page, 0)
			if opened != nil {
				opened.Close()
			}
			if err == nil {
				t.Fatal("unproved pinned facts admitted", mode)
			}
		})
	}
}
func TestScanEntryStickyTerminalFailure(t *testing.T) {
	f := newScanReadFixture(t)
	opened, err := f.r.OpenScanEntry(t.Context(), f.admit(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	f.media.terminal = errScanFixtureTerminal
	data := make([]byte, 4)
	n, err := opened.ReadAt(data, 0)
	if n != 0 || !errors.Is(err, errScanFixtureTerminal) || string(data) != "\x00\x00\x00\x00" {
		t.Fatal("terminal bytes accepted", n, data, err)
	}
	f.media.terminal = nil
	if _, err := opened.ReadAt(data, 0); !errors.Is(err, errScanFixtureTerminal) || f.media.reads.Load() != 1 {
		t.Fatal("failed pinned handle recovered", err)
	}
}

func TestScanEntryTerminalEOFClearsEarlierRanges(t *testing.T) {
	for _, method := range []string{"ReadAt", "Read"} {
		t.Run(method, func(t *testing.T) {
			for _, joined := range []bool{false, true} {
				name := "provider-eof"
				if joined {
					name = "joined-provider-eof"
				}
				t.Run(name, func(t *testing.T) {
					f := newScanReadFixture(t)
					const size = (8 << 20) + 4
					f.page.Entries[0].Size, f.media.info.Size = size, size
					f.media.data = make([]byte, size)
					for i := range f.media.data {
						f.media.data[i] = 'f'
					}
					f.media.afterRead = func() {
						if f.media.reads.Load() == 2 {
							f.media.terminal = io.EOF
							if joined {
								f.media.terminal = errors.Join(io.EOF, errScanFixtureTerminal)
							}
						}
					}
					opened, err := f.r.OpenScanEntry(t.Context(), f.admit(t), 0)
					if err != nil {
						t.Fatal(err)
					}
					defer opened.Close()
					data := make([]byte, size)
					var n int
					if method == "ReadAt" {
						n, err = opened.ReadAt(data, 0)
					} else {
						n, err = opened.Read(data)
					}
					if n != 0 || !errors.Is(err, io.EOF) || !errors.Is(opened.Err(), io.EOF) {
						t.Fatalf("provider EOF accepted earlier pinned ranges: n=%d error=%v sticky=%v", n, err, opened.Err())
					}
					for _, b := range data {
						if b != 0 {
							t.Fatal("terminal provider EOF retained prior range bytes")
						}
					}
					if joined && !errors.Is(err, errScanFixtureTerminal) {
						t.Fatal("joined terminal error was lost", err)
					}
				})
			}
		})
	}
}

func TestScanEntryNormalEOFIsNotSticky(t *testing.T) {
	f := newScanReadFixture(t)
	opened, err := f.r.OpenScanEntry(t.Context(), f.admit(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	data := make([]byte, 8)
	if n, err := opened.ReadAt(data, 0); n != 4 || !errors.Is(err, io.EOF) || string(data[:4]) != "film" || opened.Err() != nil {
		t.Fatal("ordinary end of file became terminal", n, data, err, opened.Err())
	}
	if n, err := opened.ReadAt(data, 4); n != 0 || !errors.Is(err, io.EOF) || opened.Err() != nil {
		t.Fatal("EOF offset became terminal", n, err, opened.Err())
	}
	if n, err := opened.ReadAt(data[:4], 0); n != 4 || err != nil || string(data[:4]) != "film" {
		t.Fatal("ordinary EOF invalidated a pinned handle", n, err)
	}
}
func TestScanEntryCloseJoinsReadAndHostAdmission(t *testing.T) {
	f := newScanReadFixture(t)
	opened, err := f.r.OpenScanEntry(t.Context(), f.admit(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	f.media.readStarted, f.media.readRelease = started, release
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); opened.Close() })
	readDone := make(chan error, 1)
	go func() { _, err := opened.ReadAt(make([]byte, 4), 0); readDone <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("read not admitted")
	}
	// The sole pool connection remains free while the provider is blocked.
	query, cancel := context.WithTimeout(t.Context(), time.Second)
	var one int
	err = f.r.pool.QueryRow(query, `SELECT 1`).Scan(&one)
	cancel()
	if err != nil || one != 1 {
		t.Fatal("provider retained DB connection", err)
	}
	closed := make(chan struct{})
	go func() { opened.Close(); close(closed) }()
	select {
	case <-opened.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel read")
	}
	f.r.host.mu.Lock()
	admitted := f.r.host.readers
	f.r.host.mu.Unlock()
	if admitted != 1 {
		t.Fatal("host released before read joined", admitted)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("canceled read accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not join")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not finish")
	}
	f.r.host.mu.Lock()
	admitted = f.r.host.readers
	f.r.host.mu.Unlock()
	if admitted != 0 {
		t.Fatal("host admission leaked", admitted)
	}
	if err := opened.Close(); err != nil {
		t.Fatal("idempotent close", err)
	}
}
func TestScanEntryRuntimeCancellation(t *testing.T) {
	for _, stage := range []string{"before-read", "after-read", "after-stat"} {
		t.Run(stage, func(t *testing.T) {
			f := newScanReadFixture(t)
			page := f.admit(t)
			if stage == "after-stat" {
				f.media.afterStat = f.cancelRuntime
			}
			opened, err := f.r.OpenScanEntry(t.Context(), page, 0)
			if stage == "after-stat" {
				if opened != nil {
					opened.Close()
				}
				if err == nil {
					t.Fatal("revoked runtime accepted Stat")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if stage == "before-read" {
				f.cancelRuntime()
			} else {
				f.media.afterRead = f.cancelRuntime
			}
			data := make([]byte, 4)
			if n, err := opened.ReadAt(data, 0); n != 0 || err == nil || opened.Err() == nil || string(data) != "\x00\x00\x00\x00" {
				t.Fatal("runtime revocation permitted bytes", n, data, err)
			}
			if stage == "before-read" && f.media.reads.Load() != 0 {
				t.Fatal("read ran under revoked runtime")
			}
		})
	}
}

func TestScanPageRejectsInvalidAuthorityAndEntries(t *testing.T) {
	f := newScanReadFixture(t)
	if _, err := f.r.AdmitScanPage(t.Context(), f.location, f.source, f.lease, f.checkpoint, nil); err == nil {
		t.Fatal("nil page admitted")
	}
	wrong := f.lease
	wrong.SourceKey = uuid.New()
	if _, err := f.r.AdmitScanPage(t.Context(), f.location, f.source, wrong, f.checkpoint, f.page); err == nil {
		t.Fatal("foreign lease admitted")
	}
	wrongCheckpoint := f.checkpoint
	wrongCheckpoint.Cursor = "unvalidated-cursor"
	if _, err := f.r.AdmitScanPage(t.Context(), f.location, f.source, f.lease, wrongCheckpoint, f.page); err == nil {
		t.Fatal("foreign page checkpoint admitted")
	}
	f.page.Entries[0].Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
	page := f.admit(t)
	if opened, err := f.r.OpenScanEntry(t.Context(), page, 0); err == nil {
		opened.Close()
		t.Fatal("directory opened as pinned file")
	}
	if f.media.stats.Load() != 0 {
		t.Fatal("invalid entries triggered provider I/O")
	}
}

func TestScanEntryToolInputLifetimeAndStickyFailure(t *testing.T) {
	for _, mode := range []string{"success", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			f := newScanReadFixture(t)
			opened, err := f.r.OpenScanEntry(t.Context(), f.admit(t), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			input, err := NewToolInput(opened.Context(), opened, ToolInputConfig{
				CancelSource: opened.Cancel, Reauthorize: opened.Reauthorize,
				MaxBytes: 4, ChunkBytes: 4, MaxConcurrentReads: 1, Timeout: 5 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if mode == "terminal" {
				f.media.terminal = errScanFixtureTerminal
			}
			client := toolHTTP(t)
			err = input.Run(func(_ context.Context, argument string) error {
				response, body, err := toolResponse(t, client, "GET", argument, "")
				if mode == "success" && (err != nil || response.StatusCode != 200 || body != "film") {
					return errors.New("bridge did not return successful pinned bytes")
				}
				if mode == "terminal" && body == "film" {
					t.Error("terminal pinned bytes leaked through tool input")
				}
				// A trusted tool's exit-zero result cannot clear a source failure.
				return nil
			})
			if mode == "success" && (err != nil || opened.Err() != nil) {
				t.Fatal("successful joined tool was invalidated", err, opened.Err())
			}
			if mode == "terminal" && !errors.Is(err, errScanFixtureTerminal) {
				t.Fatal("exit zero hid scan-reader terminal error", err)
			}
			f.r.host.mu.Lock()
			admitted := f.r.host.readers
			f.r.host.mu.Unlock()
			if admitted != 0 {
				t.Fatal("joined tool retained host admission", admitted)
			}
		})
	}
}
