package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// PreparedNativeS3Install owns a never-published package and final-ID state.
// It contains byte evidence, not authority. RuntimeSnapshot returns copies;
// only its originating registry can publish it, once, under fresh authority.
// Callers must fully reap their private discovery manager before publishing or
// discarding. An uncertain commit retains both executable and identity state.
type PreparedNativeS3Install struct {
	mu                                  *sync.Mutex
	identity                            *PreparedNativeS3Install
	registry                            *NativeStorageRegistry
	artifactKey                         string
	artifact                            NativeStorageArtifact
	source                              storagesource.SourceConfig
	installation                        Installation
	config                              map[string]map[string]any
	entries                             []*publicv1.ConfigEntry
	manifestJSON, archive               []byte
	statePath                           string
	packageDirectory, stateDirectory    os.FileInfo
	binaryFile, manifestFile, stateFile os.FileInfo
	stateHash                           [sha256.Size]byte
	stateCaptured                       bool
	fingerprint                         [sha256.Size]byte
	operationID                         uuid.UUID
	attempted, retained, discarded      bool
}

func initialS3Error(code string) error { return &catalog.NativeOnboardingError{Code: code} }

func nativeInstallConfigCopy(config map[string]map[string]any) (map[string]map[string]any, []*publicv1.ConfigEntry, error) {
	copied := make(map[string]map[string]any, len(config))
	entries := make([]*publicv1.ConfigEntry, 0, len(config))
	for key, value := range config {
		if value == nil {
			value = map[string]any{}
		}
		v, err := structpb.NewStruct(value)
		if err != nil {
			return nil, nil, initialS3Error("invalid_request")
		}
		copied[key] = v.AsMap()
		entries = append(entries, &publicv1.ConfigEntry{Key: key, Value: v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return copied, entries, nil
}

func (p *PreparedNativeS3Install) digest() ([sha256.Size]byte, error) {
	data, err := json.Marshal(struct {
		ArtifactKey        string
		Checksum, OS, Arch string
		Source             storagesource.SourceConfig
		Installation       Installation
		Config             map[string]map[string]any
		StatePath          string
		OperationID        uuid.UUID
		RuntimeGeneration  int64
	}{p.artifactKey, p.artifact.Checksum, p.artifact.OS, p.artifact.Arch, p.source, p.installation, p.config, p.statePath, p.operationID, p.installation.RuntimeGeneration})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	h := sha256.New()
	for _, part := range [][]byte{data, p.manifestJSON, p.archive} {
		sum := sha256.Sum256(part)
		_, _ = h.Write(sum[:])
	}
	manifest, err := proto.MarshalOptions{Deterministic: true}.Marshal(p.artifact.Manifest)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	config, err := proto.MarshalOptions{Deterministic: true}.Marshal(&publicv1.ConfigureRequest{Config: p.entries})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	for _, part := range [][]byte{manifest, config} {
		sum := sha256.Sum256(part)
		_, _ = h.Write(sum[:])
	}
	var result [sha256.Size]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

// PrepareInitialS3InstallAuthorized reserves BIGSERIAL identity without rows.
// Its short authority transaction ends before ZIP/filesystem work or provider
// startup. A reservation is never rewound, including failed preparations.
func (r *NativeStorageRegistry) PrepareInitialS3InstallAuthorized(ctx context.Context, req NativeStorageInstallRequest, authorize NativeStorageAuthorizeTx) (*PreparedNativeS3Install, error) {
	if r == nil || r.pool == nil || authorize == nil {
		return nil, nativeManagementUnavailable()
	}
	a, ok := r.artifact(req.ArtifactKey)
	if !ok || a.Manifest.GetPluginId() != nativeS3PluginID {
		return nil, initialS3Error("artifact_rejected")
	}
	a.Manifest = proto.CloneOf(a.Manifest)
	if err := validateNativeManagementConfigForOperation(a.Manifest, req.Config, "artifact_rejected"); err != nil {
		return nil, err
	}
	source := req.Source
	if source.Key == uuid.Nil || source.OwnerID == uuid.Nil || source.InstallationID != nil || source.RootEntryID != "" || (source.PluginID != "" && source.PluginID != nativeS3PluginID) || !nativeStorageText(source.ProviderSourceID) {
		return nil, initialS3Error("invalid_request")
	}
	if len(req.Binary) == 0 || len(req.Binary) > 256<<20 {
		return nil, initialS3Error("invalid_request")
	}
	binary := bytes.Clone(req.Binary)
	sum := sha256.Sum256(binary)
	goos, arch, known := nativeManagementBinaryPlatform(binary)
	if hex.EncodeToString(sum[:]) != a.Checksum || !known || goos != a.OS || arch != a.Arch {
		return nil, initialS3Error("artifact_rejected")
	}
	config, entries, err := nativeInstallConfigCopy(req.Config)
	if err != nil {
		return nil, err
	}
	// This guard retains no connection across package preparation/discovery.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	err = func() error {
		if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
			return err
		}
		if err := authorize(ctx, tx); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_sources WHERE key=$1)`, source.Key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return initialS3Error("source_attached")
		}
		return nil
	}()
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	rollbackErr := tx.Rollback(rollbackCtx)
	cancel()
	if err != nil || rollbackErr != nil {
		return nil, errors.Join(err, rollbackErr)
	}
	var reserved int64
	if err := r.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('plugin_installations','id'))`).Scan(&reserved); err != nil {
		return nil, err
	}
	id := int(reserved)
	if reserved <= 0 || int64(id) != reserved {
		return nil, nativeManagementUnavailable()
	}
	manifestJSON, err := protojson.Marshal(a.Manifest)
	if err != nil {
		return nil, err
	}
	archive, err := buildNativeStorageArchive(manifestJSON, binary)
	if err != nil {
		return nil, err
	}
	statePath, err := nativeS3ReserveState(r.baseDir, id)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(r.baseDir, "native-")
	if err != nil {
		_ = os.RemoveAll(filepath.Dir(statePath))
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
			_ = os.RemoveAll(filepath.Dir(statePath))
		}
	}()
	path := filepath.Join(dir, "plugin")
	if err := os.WriteFile(path, binary, 0500); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0400); err != nil {
		return nil, err
	}
	// Preserve the existing on-disk platform verification too.
	goos, arch, known = binaryPlatform(path)
	if !known || goos != a.OS || arch != a.Arch {
		return nil, nativeManagementUnavailable()
	}
	source.PluginID = nativeS3PluginID
	source.ConfigurationRevision = 1
	source.InstallationID = &reserved
	installation := Installation{ID: id, PluginID: nativeS3PluginID, Version: a.Manifest.GetVersion(), InstallPath: path, Enabled: source.Enabled, Kind: KindPlugin, UpdatePolicy: "manual", RuntimeGeneration: 1}
	p := &PreparedNativeS3Install{mu: &sync.Mutex{}, registry: r, artifactKey: req.ArtifactKey, artifact: a, source: source, installation: installation, config: config, entries: entries, manifestJSON: manifestJSON, archive: archive, statePath: statePath, operationID: uuid.New()}
	p.packageDirectory, err = os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	p.stateDirectory, err = os.Lstat(filepath.Dir(statePath))
	if err != nil {
		return nil, err
	}
	p.binaryFile, err = os.Lstat(path)
	if err != nil {
		return nil, err
	}
	p.manifestFile, err = os.Lstat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	p.identity = p
	p.fingerprint, err = p.digest()
	if err != nil {
		return nil, err
	}
	if _, err := r.nativeRuntimeConfig(&p.installation, p.entries); err != nil {
		return nil, err
	}
	keep = true
	return p, nil
}

// RuntimeSnapshot is a private execution view, including host-derived state.
// Desired enabled state remains in the copy; the one-shot caller explicitly
// enables only its temporary execution, without changing publication intent.
func (p *PreparedNativeS3Install) RuntimeSnapshot() (*NativeStorageSnapshot, error) {
	if p == nil || p.identity != p || p.mu == nil {
		return nil, nativeManagementUnavailable()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.identity != p || p.attempted || p.discarded {
		return nil, nativeManagementUnavailable()
	}
	digest, err := p.digest()
	if err != nil || digest != p.fingerprint {
		return nil, nativeManagementUnavailable()
	}
	return p.snapshot()
}
func (p *PreparedNativeS3Install) snapshot() (*NativeStorageSnapshot, error) {
	installation := p.installation
	source := p.source
	if source.InstallationID != nil {
		id := *source.InstallationID
		source.InstallationID = &id
	}
	entries, err := p.registry.nativeRuntimeConfig(&installation, proto.CloneOf(&publicv1.ConfigureRequest{Config: p.entries}).Config)
	if err != nil {
		return nil, err
	}
	return &NativeStorageSnapshot{Source: source, Installation: &installation, Generation: 1, ArtifactChecksum: p.artifact.Checksum, Manifest: proto.CloneOf(p.artifact.Manifest), Config: entries}, nil
}

func (r *NativeStorageRegistry) verifyInitialS3Preparation(ctx context.Context, p *PreparedNativeS3Install) error {
	digest, err := p.digest()
	a, ok := r.artifact(p.artifactKey)
	if err != nil || digest != p.fingerprint || !ok || a.Checksum != p.artifact.Checksum || a.OS != p.artifact.OS || a.Arch != p.artifact.Arch || !proto.Equal(a.Manifest, p.artifact.Manifest) {
		return nativeManagementUnavailable()
	}
	if err := initialS3DirectoryIdentity(filepath.Dir(p.statePath), p.stateDirectory); err != nil {
		return nativeManagementUnavailable()
	}
	if err := initialS3DirectoryIdentity(filepath.Dir(p.installation.InstallPath), p.packageDirectory); err != nil {
		return nativeManagementUnavailable()
	}
	state, err := nativeS3StatePath(r.baseDir, p.installation.ID)
	if err != nil || state != p.statePath {
		return nativeManagementUnavailable()
	}
	for _, file := range []struct {
		path     string
		perm     os.FileMode
		expected []byte
		identity os.FileInfo
	}{{p.installation.InstallPath, 0500, nil, p.binaryFile}, {filepath.Join(filepath.Dir(p.installation.InstallPath), "manifest.json"), 0400, p.manifestJSON, p.manifestFile}} {
		info, err := os.Lstat(file.path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != file.perm || !initialS3SameFile(file.identity, info) {
			return nativeManagementUnavailable()
		}
		opened, err := nativeS3OpenPrivateFile(file.path, r.baseDir, file.perm)
		if err != nil {
			return nativeManagementUnavailable()
		}
		openedInfo, err := opened.Stat()
		if err != nil || !initialS3SameFile(file.identity, openedInfo) {
			_ = opened.Close()
			return nativeManagementUnavailable()
		}
		data, err := io.ReadAll(opened)
		afterInfo, statErr := opened.Stat()
		closeErr := opened.Close()
		linkedInfo, linkErr := os.Lstat(file.path)
		if statErr != nil || linkErr != nil || !initialS3SameFile(file.identity, afterInfo) || !initialS3SameFile(file.identity, linkedInfo) {
			return nativeManagementUnavailable()
		}
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nativeManagementUnavailable()
		}
		if file.expected != nil {
			if !bytes.Equal(data, file.expected) {
				return nativeManagementUnavailable()
			}
		} else {
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != p.artifact.Checksum {
				return nativeManagementUnavailable()
			}
		}
	}
	if p.stateCaptured {
		hash, info, err := initialS3StateFingerprint(ctx, r.baseDir, p.statePath)
		if err != nil || !initialS3SameFile(p.stateFile, info) || hash != p.stateHash {
			return nativeManagementUnavailable()
		}
	}
	return nil
}

func initialS3SameFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && before.Mode() == after.Mode()
}
func initialS3DirectoryIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(expected, current) {
		return errors.New("prepared native directory identity changed")
	}
	return nil
}
func initialS3StateFingerprint(ctx context.Context, baseDir, path string) ([sha256.Size]byte, os.FileInfo, error) {
	var hash [sha256.Size]byte
	file, err := nativeS3OpenPrivateFile(path, baseDir, 0600)
	if err != nil {
		return hash, nil, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return hash, nil, err
	}
	h := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return hash, nil, err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return hash, nil, err
		}
	}
	after, err := file.Stat()
	if err != nil || !initialS3SameFile(before, after) {
		return hash, nil, errors.New("prepared native state changed during capture")
	}
	linked, err := os.Lstat(path)
	if err != nil || !initialS3SameFile(after, linked) {
		return hash, nil, errors.New("prepared native state path changed during capture")
	}
	copy(hash[:], h.Sum(nil))
	return hash, after, nil
}

// CaptureInitialS3DiscoveryState seals the provider's private identity file
// after its completed Describe. Publication rechecks it after reaping and row
// waits; a missing/replaced state file cannot regenerate the admitted root.
func (r *NativeStorageRegistry) CaptureInitialS3DiscoveryState(ctx context.Context, p *PreparedNativeS3Install) error {
	if r == nil || p == nil || p.identity != p || p.mu == nil {
		return nativeManagementUnavailable()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.registry != r || p.attempted || p.discarded || p.stateCaptured {
		return nativeManagementUnavailable()
	}
	if err := r.verifyInitialS3Preparation(ctx, p); err != nil {
		return err
	}
	hash, info, err := initialS3StateFingerprint(ctx, r.baseDir, p.statePath)
	if err != nil {
		return err
	}
	p.stateHash, p.stateFile, p.stateCaptured = hash, info, true
	return nil
}

// PublishInitialS3InstallAuthorized consumes the owned preparation once, after
// the discovery process is reaped. It is new-only; a retained source collision
// cannot be reattached. Fresh authority is retained again after all row waits.
func (r *NativeStorageRegistry) PublishInitialS3InstallAuthorized(ctx context.Context, p *PreparedNativeS3Install, root string, authorize NativeStorageAuthorizeTx) (*NativeStorageSnapshot, error) {
	if r == nil || r.pool == nil || p == nil || p.identity != p || p.mu == nil || authorize == nil {
		return nil, nativeManagementUnavailable()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.identity != p || p.registry != r || p.attempted || p.discarded {
		return nil, nativeManagementUnavailable()
	}
	p.attempted = true
	if !nativeStorageText(root) || !p.stateCaptured {
		return nil, nativeManagementUnavailable()
	}
	if err := r.verifyInitialS3Preparation(ctx, p); err != nil {
		return nil, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() {
		bounded, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(bounded)
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, err
	}
	if err := authorize(ctx, tx); err != nil {
		return nil, err
	}
	if err := r.verifyInitialS3Preparation(ctx, p); err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_sources WHERE key=$1)`, p.source.Key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, initialS3Error("source_attached")
	}
	installation, err := scanInstallation(tx.QueryRow(ctx, `INSERT INTO plugin_installations(id,plugin_id,version,install_path,enabled,update_policy,owner_id,runtime_generation) VALUES($1,$2,$3,$4,$5,'manual',$6,1) RETURNING `+installationColumns, p.installation.ID, p.source.PluginID, p.installation.Version, p.installation.InstallPath, p.source.Enabled, p.source.OwnerID))
	if err != nil {
		return nil, err
	}
	id := int64(installation.ID)
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, id, p.source.OwnerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO plugin_archives(plugin_installation_id,manifest_json,checksum,archive_bytes) VALUES($1,$2,$3,$4)`, id, p.manifestJSON, p.artifact.Checksum, p.archive); err != nil {
		return nil, err
	}
	if err := r.replaceConfigTx(ctx, tx, installation.ID, p.config); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_sources(key,owner_id,installation_id,latest_installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled) VALUES($1,$2,$3,$3,$4,$5,$6,1,$7)`, p.source.Key, p.source.OwnerID, id, p.source.PluginID, p.source.ProviderSourceID, root, p.source.Enabled); err != nil {
		return nil, err
	}
	if err := authorize(ctx, tx); err != nil {
		return nil, err
	}
	if err := r.verifyInitialS3Preparation(ctx, p); err != nil {
		return nil, err
	}
	snapshot, err := p.snapshot()
	if err != nil {
		return nil, err
	}
	snapshot.Installation = installation
	snapshot.Source.RootEntryID = root
	// Row locks prevent concurrent revocation, but clock-based authority can
	// expire during final file hashing and snapshot construction. Recheck at
	// the commit boundary after that work has completed.
	if err := authorize(ctx, tx); err != nil {
		return nil, err
	}
	p.retained = true // all outcomes other than positive rollback retain files
	if r.commitInstall != nil {
		err = r.commitInstall(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			p.retained = false
		}
		return nil, nativeManagementCommitError(err, "install", p.source.Key, p.operationID)
	}
	return snapshot, nil
}

// DiscardInitialS3Install must run only after discovery has fully reaped. It is
// harmless after publication or an uncertain commit, whose files remain owned.
func (r *NativeStorageRegistry) DiscardInitialS3Install(p *PreparedNativeS3Install) error {
	if p == nil {
		return nil
	}
	if p.identity != p || p.mu == nil {
		return nativeManagementUnavailable()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.identity != p || p.registry != r {
		return nativeManagementUnavailable()
	}
	if p.retained || p.discarded {
		return nil
	}
	p.discarded = true
	return errors.Join(
		initialS3DiscardDirectory(filepath.Dir(p.installation.InstallPath), p.packageDirectory, map[string]os.FileInfo{"plugin": p.binaryFile, "manifest.json": p.manifestFile}),
		initialS3DiscardDirectory(filepath.Dir(p.statePath), p.stateDirectory, map[string]os.FileInfo{"index.db": p.stateFile}))
}

// A replacement is not this preparation's property. Keep it and report the
// private cleanup failure instead of deleting a different package or state.
func initialS3DiscardDirectory(path string, expected os.FileInfo, files map[string]os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.IsDir() || !os.SameFile(expected, current) {
		return errors.New("prepared native directory replacement retained")
	}
	for name, identity := range files {
		if identity == nil {
			continue
		}
		info, err := os.Lstat(filepath.Join(path, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !initialS3SameFile(identity, info) {
			return errors.New("prepared native file replacement retained")
		}
	}
	return os.RemoveAll(path)
}
