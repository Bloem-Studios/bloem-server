package nativestorage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/encoding/protojson"
)

// LoadApprovals reads an optional host-selected, protected immutable startup
// record. There is no request/config-store approval path and no download. Empty
// path means no approved artifacts; an explicit invalid path fails startup.
// Registry construction validates and clones the reviewed manifest/checksum.
func LoadApprovals(path string) (map[string]plugins.NativeStorageArtifact, error) {
	result := make(map[string]plugins.NativeStorageArtifact)
	if path == "" {
		return result, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("approval file must be absolute and clean")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if real != path {
		return nil, errors.New("approval path must not contain symlinks")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) || (info.Mode().Perm()&0022 != 0 && (stat.Uid != 0 || info.Mode()&os.ModeSticky == 0)) {
			return nil, errors.New("approval parent directory is not host protected")
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Mode().Perm()&0077 != 0 || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) || info.Size() > 1<<20 {
		return nil, errors.New("approval file must be host-owned, private, read-only and bounded")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("approval record exceeds bound")
	}
	var records map[string]struct {
		Manifest json.RawMessage `json:"manifest"`
		Checksum string          `json:"checksum"`
		OS       string          `json:"os"`
		Arch     string          `json:"arch"`
	}
	if err = json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	for key, record := range records {
		manifest := new(publicv1.PluginManifest)
		if err = protojson.Unmarshal(record.Manifest, manifest); err != nil {
			return nil, err
		}
		result[key] = plugins.NativeStorageArtifact{Manifest: manifest, Checksum: record.Checksum, OS: record.OS, Arch: record.Arch}
	}
	return result, nil
}

type Host struct {
	mu       sync.Mutex
	closed   bool
	readers  int
	drained  chan struct{}
	Registry *plugins.NativeStorageRegistry
	Manager  *storageplugin.Manager
	// covers is the router's coordinator, shared by every listener that
	// serves artwork, so covers open under the same mutation fence as reads.
	covers atomic.Pointer[Coordinator]
	// coverSlots bounds concurrent cover reads on this node, so a page of
	// covers cannot flood a provider.
	coverSlots chan struct{}
}

// maxConcurrentCovers bounds cover reads per node; a cover takes tens of
// milliseconds, so this serves a few hundred covers a second.
const maxConcurrentCovers = 16

// SetCoverReader publishes the coordinator that opens storage covers.
func (h *Host) SetCoverReader(c *Coordinator) {
	if h != nil {
		h.covers.Store(c)
	}
}

// OpenCover opens a book's storage cover through the published coordinator.
func (h *Host) OpenCover(ctx context.Context, contentID, revision string) (mediasource.File, error) {
	if h == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	c := h.covers.Load()
	if c == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	if h.coverSlots == nil {
		return c.OpenCover(ctx, contentID, revision)
	}
	select {
	case h.coverSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	file, err := c.OpenCover(ctx, contentID, revision)
	if err != nil {
		<-h.coverSlots
		return nil, err
	}
	return &coverSlotFile{File: file, release: func() { <-h.coverSlots }}, nil
}

// coverSlotFile returns its cover slot when closed.
type coverSlotFile struct {
	mediasource.File
	release func()
	once    sync.Once
}

func (f *coverSlotFile) Close() error {
	err := f.File.Close()
	f.once.Do(f.release)
	return err
}

func NewHost(pool *pgxpool.Pool, cipher *secret.Cipher, installRoot, approvalPath string) (*Host, error) {
	approved, err := LoadApprovals(approvalPath)
	if err != nil {
		return nil, err
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, installRoot, approved)
	if err != nil {
		return nil, err
	}
	return &Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{}), coverSlots: make(chan struct{}, maxConcurrentCovers)}, nil
}

// AcquireOpen retains the runtime until the response closes its file. It also
// covers authorization/startup failures, whose caller must release admission.
func (h *Host) AcquireOpen() (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, storageplugin.ErrClosed
	}
	if h.readers == 0 {
		h.drained = make(chan struct{})
	}
	h.readers++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.readers--
			if h.readers == 0 {
				close(h.drained)
			}
		})
	}, nil
}

// Shutdown closes admission and waits for actual native responses/startups to
// release ownership before reaping processes. A drain timeout leaves the
// runtime alive for those workers; a later call can finish shutdown.
func (h *Host) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.closed = true
	drained := h.drained
	readers := h.readers
	h.mu.Unlock()
	if readers != 0 {
		select {
		case <-drained:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return h.Manager.Shutdown(ctx)
}
