// Package storageplugin owns isolated native-only storage provider processes.
// It never performs tenant authorization or registers privileged RuntimeHost
// callbacks. Callers must authorize snapshots in the host repository first.
package storageplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

var (
	ErrDisabled        = errors.New("native storage installation disabled")
	ErrClosed          = errors.New("native storage runtime closed")
	ErrStaleGeneration = errors.New("native storage generation is stale")
	ErrSnapshotChanged = errors.New("native storage snapshot changed without a new generation")
	ErrUnavailable     = errors.New("native storage provider unavailable")
	ErrRestartBackoff  = errors.New("native storage restart backoff active")
	ErrRestartLimit    = errors.New("native storage restart budget exhausted")
)

// Snapshot is an already authorized, immutable installation/configuration view.
// The repository must check source/resource ownership, membership, entitlements,
// enabled state, and the native-only registry before supplying it. InstallationID
// identifies a runtime slot; matching IDs does not establish authorization.
// Generation must increase on artifact/configuration changes and re-enablement.
// BinaryPath must name a host-owned immutable installed executable.
type Snapshot struct {
	InstallationID   int
	Generation       uint64
	BinaryPath       string
	ExpectedChecksum string
	Manifest         *publicv1.PluginManifest
	Config           []*publicv1.ConfigEntry
	Enabled          bool
	NativeOnly       bool
}
type Config struct {
	StartTimeout           time.Duration
	RPCDeadline            time.Duration
	HealthInterval         time.Duration
	HealthFailureLimit     int
	RestartBackoff         time.Duration
	MaxStartsPerGeneration int
}
type Manager struct {
	mu     sync.Mutex
	config Config
	slots  map[int]*slot
	closed bool
}
type slot struct {
	snapshot    Snapshot
	ctx         context.Context
	cancel      context.CancelFunc
	ready       chan struct{}
	done        chan struct{}
	session     *Session
	err         error
	disabled    bool
	restartable bool
	attempts    int
	nextRestart time.Time
}
type Session struct {
	ctx      context.Context
	done     <-chan struct{}
	provider storagev1.StorageProviderClient
	process  *plugin.Client
	command  *exec.Cmd
}

func (s *Session) Provider() storagev1.StorageProviderClient { return s.provider }
func (s *Session) Context() context.Context                  { return s.ctx }

// Done closes after the process has exited and its wait state has been reaped.
func (s *Session) Done() <-chan struct{} { return s.done }

func NewManager(config Config) *Manager {
	if config.StartTimeout <= 0 {
		config.StartTimeout = 10 * time.Second
	}
	if config.RPCDeadline <= 0 {
		config.RPCDeadline = 10 * time.Second
	}
	if config.HealthInterval <= 0 {
		config.HealthInterval = 10 * time.Second
	}
	if config.HealthFailureLimit <= 0 {
		config.HealthFailureLimit = 3
	}
	if config.RestartBackoff <= 0 {
		config.RestartBackoff = time.Second
	}
	if config.MaxStartsPerGeneration <= 0 {
		config.MaxStartsPerGeneration = 3
	}
	return &Manager{config: config, slots: make(map[int]*slot)}
}
func cloneSnapshot(req Snapshot) Snapshot {
	if req.Manifest != nil {
		req.Manifest = proto.CloneOf(req.Manifest)
	}
	config := proto.CloneOf(&publicv1.ConfigureRequest{Config: req.Config})
	req.Config = config.Config
	return req
}
func sameSnapshot(a, b Snapshot) bool {
	return a.BinaryPath == b.BinaryPath && a.ExpectedChecksum == b.ExpectedChecksum &&
		a.Enabled == b.Enabled && a.NativeOnly == b.NativeOnly &&
		proto.Equal(a.Manifest, b.Manifest) &&
		proto.Equal(&publicv1.ConfigureRequest{Config: a.Config}, &publicv1.ConfigureRequest{Config: b.Config})
}
func validateSnapshot(req Snapshot) error {
	if req.InstallationID <= 0 || req.Generation == 0 {
		return errors.New("native storage installation and generation required")
	}
	if !req.Enabled {
		return ErrDisabled
	}
	if !req.NativeOnly {
		return errors.New("installation is not registered native-only")
	}
	if req.Manifest == nil {
		return errors.New("installed manifest required")
	}
	if err := publicmanifest.Validate(req.Manifest); err != nil {
		return fmt.Errorf("invalid native manifest: %w", err)
	}
	if req.Manifest.GetPluginId() == "silo.builtin" {
		return errors.New("reserved built-in plugin identity")
	}
	if req.Manifest.GetSiloApiVersion() != "v1" {
		return errors.New("unsupported public runtime API")
	}
	if len(req.Manifest.GetCapabilities()) != 0 || len(req.Manifest.GetHttpRoutes()) != 0 {
		return errors.New("native-only provider must not declare public capabilities or routes")
	}
	digest, err := hex.DecodeString(req.ExpectedChecksum)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("selected executable SHA-256 required")
	}
	if req.Manifest.GetChecksum() != req.ExpectedChecksum {
		return errors.New("installed manifest and selected executable checksum differ")
	}
	supported := false
	for _, platform := range req.Manifest.GetSupportedPlatforms() {
		if platform.GetOs() == runtime.GOOS && platform.GetArch() == runtime.GOARCH {
			supported = true
		}
	}
	if !supported {
		return errors.New("native provider platform unsupported")
	}
	if !filepath.IsAbs(req.BinaryPath) || filepath.Clean(req.BinaryPath) != req.BinaryPath {
		return errors.New("installed executable path must be absolute and clean")
	}
	info, err := os.Lstat(req.BinaryPath)
	if err != nil {
		return fmt.Errorf("installed executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("installed executable must be a regular executable file")
	}
	if proto.Size(&publicv1.ConfigureRequest{Config: req.Config}) > 1<<20 {
		return errors.New("source configuration exceeds limit")
	}
	return nil
}

// Ensure deduplicates startup and returns only a configured, verified provider.
// Request cancellation cancels a startup it initiated, but never a resident
// session borrowed by that request. Sessions live until disable, replacement,
// process/health failure, or Shutdown. A failed startup or resident may be relaunched only
// by a subsequent Ensure, with a finite per-generation budget and backoff.
func (m *Manager) Ensure(ctx context.Context, req Snapshot) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req = cloneSnapshot(req)
	if req.InstallationID <= 0 || req.Generation == 0 {
		return nil, errors.New("native storage installation and generation required")
	}
	if !req.Enabled {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			return nil, ErrClosed
		}
		if old := m.slots[req.InstallationID]; old != nil && req.Generation < old.snapshot.Generation {
			return nil, ErrStaleGeneration
		}
		m.disableLocked(req.InstallationID, req.Generation)
		return nil, ErrDisabled
	}
	if err := validateSnapshot(req); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	old := m.slots[req.InstallationID]
	attempts := 1
	if old != nil {
		if req.Generation < old.snapshot.Generation {
			m.mu.Unlock()
			return nil, ErrStaleGeneration
		}
		if req.Generation == old.snapshot.Generation {
			if old.disabled {
				m.mu.Unlock()
				return nil, ErrDisabled
			}
			if !sameSnapshot(req, old.snapshot) {
				m.mu.Unlock()
				return nil, ErrSnapshotChanged
			}
			if old.err == nil {
				m.mu.Unlock()
				return m.await(ctx, old)
			}
			if !old.restartable {
				err := old.err
				m.mu.Unlock()
				return nil, err
			}
			if old.attempts >= m.config.MaxStartsPerGeneration {
				m.mu.Unlock()
				return nil, ErrRestartLimit
			}
			if time.Now().Before(old.nextRestart) {
				m.mu.Unlock()
				return nil, ErrRestartBackoff
			}
			attempts = old.attempts + 1
		}
		old.cancel()
	}
	lifetime, cancel := context.WithCancel(context.Background())
	current := &slot{snapshot: req, ctx: lifetime, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), attempts: attempts}
	m.slots[req.InstallationID] = current
	m.mu.Unlock()
	// A replacement waits for old.Done, so two processes never overlap for a slot.
	go m.run(ctx, current, old)
	return m.await(ctx, current)
}
func (m *Manager) await(ctx context.Context, s *slot) (*Session, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ready:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if err := s.ctx.Err(); err != nil {
		return nil, ErrUnavailable
	}
	return s.session, nil
}
func (m *Manager) run(request context.Context, s, old *slot) {
	stopCancellation := context.AfterFunc(request, s.cancel)
	defer stopCancellation()
	defer close(s.done)
	if old != nil {
		// Even a canceled replacement must retain the reaping chain. Otherwise
		// Shutdown could return while a superseded process is still exiting.
		<-old.done
	}
	session, err := m.start(s)
	// Detach the initiating request only after startup is fully complete.
	stopCancellation()
	if err == nil {
		err = s.ctx.Err()
	}
	m.mu.Lock()
	s.session = session
	if err != nil {
		s.err = fmt.Errorf("%w: startup failed", ErrUnavailable)
	}
	close(s.ready)
	m.mu.Unlock()
	if err == nil {
		err = m.monitor(s, session)
	}
	m.mu.Lock()
	if s.err == nil {
		s.err = ErrUnavailable
	}
	// A canceled initiating request may retry the same immutable generation.
	// Disable/replacement/shutdown revoke the slot instead, so they cannot retry.
	s.restartable = err != nil && m.slots[s.snapshot.InstallationID] == s && !m.closed && !s.disabled
	s.nextRestart = time.Now().Add(m.config.RestartBackoff)
	s.cancel()
	m.mu.Unlock()
	if session != nil {
		session.process.Kill()
	}
}
func (m *Manager) start(s *slot) (*Session, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	req := s.snapshot
	digest, _ := hex.DecodeString(req.ExpectedChecksum)
	var command *exec.Cmd
	process := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: sdkruntime.HandshakeConfig(),
		Plugins:         sdkruntime.DefaultPluginSet(sdkruntime.CapabilityServers{}),
		SkipHostEnv:     true, StartTimeout: m.config.StartTimeout,
		// RunnerFunc receives a transport spec whose Stdin go-plugin replaces
		// with os.Stdin. Never execute that spec or copy its descriptors.
		RunnerFunc: func(_ hclog.Logger, spec *exec.Cmd, tmpDir string) (runner.Runner, error) {
			created := false
			defer func() {
				// Before a runner is returned, go-plugin has no runner ID
				// and cannot remove its already-created socket directory.
				if !created {
					_ = os.RemoveAll(tmpDir)
				}
			}()

			// v1.7.0 checks SecureConfig on its dummy empty Path before this
			// callback. Perform the identical check on the actual executable.
			// Installed artifacts/directories must be host-owned and immutable
			// between this path-based hash and exec; this is not an OS sandbox.
			secure := &plugin.SecureConfig{Checksum: digest, Hash: sha256.New()}
			if ok, err := secure.Check(req.BinaryPath); err != nil {
				return nil, fmt.Errorf("error verifying checksum: %w", err)
			} else if !ok {
				return nil, plugin.ErrChecksumsDoNotMatch
			}
			command = exec.CommandContext(s.ctx, req.BinaryPath)
			command.Env = append([]string{"LANG=C", "LC_ALL=C", "TZ=UTC"}, spec.Env...)
			// nil Stdin makes os/exec attach the null device.
			owned, err := newProcessRunner(command)
			if err != nil {
				return nil, err
			}
			created = true
			return owned, nil
		},
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           hclog.NewNullLogger(),
		GRPCDialOptions: []grpc.DialOption{
			grpc.WithChainUnaryInterceptor(unaryLifetime(s.ctx, m.config.RPCDeadline)),
			grpc.WithChainStreamInterceptor(streamLifetime(s.ctx, m.config.RPCDeadline)),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1 << 20)),
		},
	})
	retained := false
	defer func() {
		if !retained {
			process.Kill()
		}
	}()
	protocol, err := process.Client()
	if err != nil {
		return nil, err
	}
	raw, err := protocol.Dispense(sdkruntime.PluginSetName)
	if err != nil {
		return nil, err
	}
	client, ok := raw.(*sdkruntime.Client)
	if !ok {
		return nil, errors.New("unexpected public runtime")
	}
	manifest, err := client.Runtime().GetManifest(s.ctx, &publicv1.GetManifestRequest{})
	if err != nil {
		return nil, err
	}
	if !proto.Equal(req.Manifest, manifest.GetManifest()) {
		return nil, errors.New("embedded and installed manifests differ")
	}
	// Intentionally no BindHostBroker or general RuntimeHost service registration.
	if _, err = client.Runtime().Configure(s.ctx, &publicv1.ConfigureRequest{Config: req.Config}); err != nil {
		return nil, err
	}
	provider := storagev1.NewStorageProviderClient(client.Conn())
	description, err := provider.Describe(s.ctx, &storagev1.DescribeRequest{})
	if err != nil {
		return nil, err
	}
	if description.GetRevision() != 1 {
		return nil, errors.New("unsupported private storage protocol")
	}
	retained = true
	return &Session{ctx: s.ctx, done: s.done, provider: provider, process: process, command: command}, nil
}
func (m *Manager) monitor(s *slot, session *Session) error {
	ticker := time.NewTicker(m.config.HealthInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-ticker.C:
			if session.process.Exited() {
				return ErrUnavailable
			}
			description, err := session.provider.Describe(s.ctx, &storagev1.DescribeRequest{})
			if err != nil || description.GetRevision() != 1 {
				failures++
			} else {
				failures = 0
			}
			if failures >= m.config.HealthFailureLimit {
				return ErrUnavailable
			}
		}
	}
}

// Disable immediately fences both startup and resident RPCs. Re-enabling the
// installation requires an authorized snapshot with a higher generation.
func (m *Manager) Disable(installationID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	generation := uint64(0)
	if old := m.slots[installationID]; old != nil {
		generation = old.snapshot.Generation
	}
	m.disableLocked(installationID, generation)
}

// Keep a tombstone even with no resident process. A disabled Snapshot supplies
// its authoritative generation; Disable retains the last observed generation.
// The repository remains responsible for rejecting stale authorization views.
func (m *Manager) disableLocked(installationID int, generation uint64) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ready, done := make(chan struct{}), make(chan struct{})
	close(ready)
	if old := m.slots[installationID]; old != nil {
		old.cancel()
		done = old.done
	} else {
		close(done)
	}
	m.slots[installationID] = &slot{
		snapshot: Snapshot{InstallationID: installationID, Generation: generation},
		ctx:      ctx, cancel: cancel, ready: ready, done: done,
		disabled: true, err: ErrDisabled,
	}
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	done := make([]<-chan struct{}, 0, len(m.slots))
	for _, s := range m.slots {
		s.cancel()
		done = append(done, s.done)
	}
	m.mu.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
