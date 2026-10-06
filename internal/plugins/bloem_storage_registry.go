package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// NativeStorageArtifact is a host-approved immutable release, never supplied by
// an installation request. The map key is an opaque host-selected artifact key.
type NativeStorageArtifact struct {
	Manifest           *publicv1.PluginManifest
	Checksum, OS, Arch string
}

// NativeStorageRegistry is for TRUSTED host-authorized callers only. Owner UUID
// equality is a consistency check, never membership, entitlement or permission.
// Callers must authorize the source resource and folder before every operation.
// No method starts/stops processes: after mutation the host must cancel sessions
// and refresh the runtime using the returned durable generation.
type NativeStorageRegistry struct {
	pool     *pgxpool.Pool
	configs  *RuntimeConfigStore
	baseDir  string
	approved map[string]NativeStorageArtifact
	// commitInstall defaults to pgx.Tx.Commit; kept per registry for fault injection.
	commitInstall func(context.Context, pgx.Tx) error
}

type NativeStorageInstallRequest struct {
	ArtifactKey string
	Binary      []byte
	Source      storagesource.SourceConfig
	Config      map[string]map[string]any
}

type NativeStorageSnapshot struct {
	Source           storagesource.SourceConfig
	Installation     *Installation
	Generation       uint64
	ArtifactChecksum string
	Manifest         *publicv1.PluginManifest
	Config           []*publicv1.ConfigEntry
}

func NewNativeStorageRegistry(pool *pgxpool.Pool, cipher *secret.Cipher, baseDir string, approved map[string]NativeStorageArtifact) (*NativeStorageRegistry, error) {
	if pool == nil || cipher == nil || !filepath.IsAbs(baseDir) || filepath.Clean(baseDir) != baseDir {
		return nil, errors.New("native registry requires database, data cipher and absolute clean install root")
	}
	approvals, err := newNativeStorageApprovals(approved)
	if err != nil {
		return nil, err
	}
	return &NativeStorageRegistry{pool: pool, configs: NewRuntimeConfigStore(pool, cipher), baseDir: baseDir, approved: approvals}, nil
}
func newNativeStorageApprovals(input map[string]NativeStorageArtifact) (map[string]NativeStorageArtifact, error) {
	result := make(map[string]NativeStorageArtifact, len(input))
	for key, a := range input {
		if !nativeStorageText(key) {
			return nil, errors.New("invalid approved artifact key")
		}
		if err := validateNativeStorageArtifact(a); err != nil {
			return nil, err
		}
		a.Manifest = proto.CloneOf(a.Manifest)
		result[key] = a
	}
	return result, nil
}
func validateNativeStorageArtifact(a NativeStorageArtifact) error {
	if a.Manifest == nil {
		return errors.New("native manifest required")
	}
	if err := publicmanifest.Validate(a.Manifest); err != nil {
		return err
	}
	m := a.Manifest
	if m.GetPluginId() == "silo.builtin" || m.GetSiloApiVersion() != "v1" || len(m.GetCapabilities()) != 0 || len(m.GetHttpRoutes()) != 0 {
		return errors.New("native-only manifest declares reserved identity, unsupported API or public surfaces")
	}
	digest, err := hex.DecodeString(a.Checksum)
	if err != nil || len(digest) != sha256.Size || strings.ToLower(a.Checksum) != a.Checksum || m.GetChecksum() != a.Checksum {
		return errors.New("approved native executable SHA-256 required")
	}
	if a.OS != runtime.GOOS || a.Arch != runtime.GOARCH {
		return errors.New("approved native artifact is for another host platform")
	}
	supported := false
	for _, p := range m.GetSupportedPlatforms() {
		if p.GetOs() == a.OS && p.GetArch() == a.Arch {
			supported = true
		}
	}
	if !supported {
		return errors.New("native manifest does not support approved platform")
	}
	if len(m.GetAssets()) != 0 {
		return errors.New("native binary package cannot include assets")
	}
	return nil
}
func nativeStorageText(s string) bool {
	return s != "" && len(s) <= 1024 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func (r *NativeStorageRegistry) NativeStorageIDs(ctx context.Context, ids []int) (map[int]bool, error) {
	result := make(map[int]bool)
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT installation_id FROM bloem_storage_installations WHERE installation_id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// Install atomically publishes the installation, explicit marker, ZIP archive,
// encrypted config and retained source. It creates no public capabilities.
func (r *NativeStorageRegistry) Install(ctx context.Context, req NativeStorageInstallRequest) (*NativeStorageSnapshot, error) {
	a, ok := r.approved[req.ArtifactKey]
	if !ok {
		return nil, errors.New("native artifact is not approved")
	}
	s := req.Source
	if s.OwnerID == uuid.Nil || s.InstallationID != nil || (s.PluginID != "" && s.PluginID != a.Manifest.GetPluginId()) || !nativeStorageText(s.ProviderSourceID) || !nativeStorageText(s.RootEntryID) {
		return nil, errors.New("invalid native source ownership or identity")
	}
	if len(req.Binary) == 0 || len(req.Binary) > 256<<20 {
		return nil, errors.New("native executable size exceeds package bound")
	}
	sum := sha256.Sum256(req.Binary)
	if hex.EncodeToString(sum[:]) != a.Checksum {
		return nil, errors.New("native binary checksum mismatch")
	}
	if s.Key == uuid.Nil {
		s.Key = uuid.New()
	}
	s.PluginID = a.Manifest.GetPluginId()
	s.ConfigurationRevision = 1
	manifestJSON, err := protojson.Marshal(a.Manifest)
	if err != nil {
		return nil, err
	}
	archive, err := buildNativeStorageArchive(manifestJSON, req.Binary)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(r.baseDir, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(r.baseDir, "native-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, "plugin")
	if err = os.WriteFile(path, req.Binary, 0500); err != nil {
		return nil, err
	}
	goos, arch, known := binaryPlatform(path)
	if !known || goos != a.OS || arch != a.Arch {
		return nil, errors.New("native binary executable platform mismatch")
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0400); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// A retained detached source can be reinstalled without losing its bindings
	// or catalog/progress identity. Existing live associations cannot be stolen.
	var retained bool
	var oldOwner uuid.UUID
	var oldInstallation *int64
	var oldPlugin string
	var oldEnabled bool
	var oldRevision int64
	err = tx.QueryRow(ctx, `SELECT owner_id,installation_id,plugin_id,enabled,configuration_revision FROM bloem_storage_sources WHERE key=$1 FOR UPDATE`, s.Key).Scan(&oldOwner, &oldInstallation, &oldPlugin, &oldEnabled, &oldRevision)
	if err == nil {
		if oldOwner != s.OwnerID || oldInstallation != nil || oldEnabled || oldPlugin != s.PluginID {
			return nil, storagesource.ErrSourceUnavailable
		}
		retained = true
		s.ConfigurationRevision = oldRevision + 1
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	installation, err := scanInstallation(tx.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path,enabled,update_policy,owner_id,runtime_generation) VALUES($1,$2,$3,$4,'manual',$5,1) RETURNING `+installationColumns, s.PluginID, a.Manifest.GetVersion(), path, s.Enabled, s.OwnerID))
	if err != nil {
		return nil, err
	}
	id := int64(installation.ID)
	s.InstallationID = &id
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)`, id, s.OwnerID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO plugin_archives(plugin_installation_id,manifest_json,checksum,archive_bytes) VALUES($1,$2,$3,$4)`, id, manifestJSON, a.Checksum, archive); err != nil {
		return nil, err
	}
	if err = r.replaceConfigTx(ctx, tx, installation.ID, req.Config); err != nil {
		return nil, err
	}
	if retained {
		_, err = tx.Exec(ctx, `UPDATE bloem_storage_sources SET installation_id=$2,provider_source_id=$3,root_entry_id=$4,configuration_revision=$5,enabled=$6 WHERE key=$1`, s.Key, id, s.ProviderSourceID, s.RootEntryID, s.ConfigurationRevision, s.Enabled)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_sources(key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, s.Key, s.OwnerID, id, s.PluginID, s.ProviderSourceID, s.RootEntryID, s.ConfigurationRevision, s.Enabled)
	}
	if err != nil {
		return nil, err
	}
	entries := make([]*publicv1.ConfigEntry, 0, len(req.Config))
	for key, value := range req.Config {
		if value == nil {
			value = map[string]any{}
		}
		v, err := structpb.NewStruct(value)
		if err != nil {
			return nil, err
		}
		entries = append(entries, &publicv1.ConfigEntry{Key: key, Value: v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	// Once COMMIT is attempted, a transport error cannot prove rollback.
	// Retain the owned package so a committed installation stays runnable;
	// cleanup of unreferenced directories is a separate deferred gate.
	keep = true
	if r.commitInstall != nil {
		err = r.commitInstall(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			keep = false
		}
		return nil, err
	}
	// A disabled install is valid but intentionally cannot provide a runnable snapshot.
	return &NativeStorageSnapshot{Source: s, Installation: installation, Generation: uint64(installation.RuntimeGeneration), ArtifactChecksum: a.Checksum, Manifest: proto.CloneOf(a.Manifest), Config: entries}, nil
}
func (r *NativeStorageRegistry) replaceConfigTx(ctx context.Context, tx pgx.Tx, id int, config map[string]map[string]any) error {
	if _, err := tx.Exec(ctx, `DELETE FROM plugin_runtime_configs WHERE plugin_installation_id=$1`, id); err != nil {
		return err
	}
	for key, value := range config {
		if !nativeStorageText(key) {
			return errors.New("invalid native config key")
		}
		data, err := encodeRuntimeConfigValue(r.configs.cipher, id, key, value)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO plugin_runtime_configs(plugin_installation_id,config_key,config_value) VALUES($1,$2,$3)`, id, key, data); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot reads one MVCC view and checks retained ownership, availability,
// registry, archive approval and generation. It returns decrypted config only to
// the host-authorized runtime caller. Runtime launch still verifies executable
// bytes and the embedded manifest before Configure.
func (r *NativeStorageRegistry) Snapshot(ctx context.Context, key, owner uuid.UUID) (*NativeStorageSnapshot, error) {
	if key == uuid.Nil || owner == uuid.Nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	s, err := nativeStorageSource(ctx, tx, key, owner, false)
	if err != nil {
		return nil, err
	}
	if !s.Enabled || s.InstallationID == nil {
		return nil, storagesource.ErrSourceUnavailable
	}
	installation, err := r.checkInstallation(ctx, tx, int(*s.InstallationID), owner, false)
	if err != nil {
		return nil, err
	}
	if !installation.Enabled || installation.PluginID != s.PluginID || installation.RuntimeGeneration <= 0 {
		return nil, storagesource.ErrSourceUnavailable
	}
	archive, err := scanArchive(tx.QueryRow(ctx, `SELECT `+archiveColumns+` FROM plugin_archives WHERE plugin_installation_id=$1`, installation.ID))
	if err != nil {
		return nil, err
	}
	var manifest publicv1.PluginManifest
	if err = protojson.Unmarshal(archive.ManifestJSON, &manifest); err != nil {
		return nil, err
	}
	approved := false
	for _, a := range r.approved {
		if a.Checksum == archive.Checksum && proto.Equal(a.Manifest, &manifest) {
			approved = true
			break
		}
	}
	if err = validateNativeStorageArchive(archive.Bytes, &manifest, archive.Checksum); err != nil {
		return nil, err
	}
	if !approved || manifest.GetPluginId() != s.PluginID || manifest.GetVersion() != installation.Version {
		return nil, errors.New("native archive is not an approved installed artifact")
	}
	if !filepath.IsAbs(installation.InstallPath) || filepath.Clean(installation.InstallPath) != installation.InstallPath {
		return nil, errors.New("invalid native install path")
	}
	rel, err := filepath.Rel(r.baseDir, installation.InstallPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("native install path escapes owned root")
	}
	rows, err := tx.Query(ctx, `SELECT config_key,config_value FROM plugin_runtime_configs WHERE plugin_installation_id=$1 ORDER BY config_key`, installation.ID)
	if err != nil {
		return nil, err
	}
	entries := make([]*publicv1.ConfigEntry, 0)
	for rows.Next() {
		var key string
		var data []byte
		if err = rows.Scan(&key, &data); err != nil {
			rows.Close()
			return nil, err
		}
		value, err := decodeRuntimeConfigValue(r.configs.cipher, installation.ID, key, data)
		if err != nil {
			rows.Close()
			return nil, err
		}
		v, err := structpb.NewStruct(value)
		if err != nil {
			rows.Close()
			return nil, err
		}
		entries = append(entries, &publicv1.ConfigEntry{Key: key, Value: v})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &NativeStorageSnapshot{Source: s, Installation: installation, Generation: uint64(installation.RuntimeGeneration), ArtifactChecksum: archive.Checksum, Manifest: &manifest, Config: entries}, nil
}
func nativeStorageSource(ctx context.Context, tx pgx.Tx, key, owner uuid.UUID, lock bool) (storagesource.SourceConfig, error) {
	var s storagesource.SourceConfig
	query := `SELECT key,owner_id,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled FROM bloem_storage_sources WHERE key=$1 AND owner_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.QueryRow(ctx, query, key, owner).Scan(&s.Key, &s.OwnerID, &s.InstallationID, &s.PluginID, &s.ProviderSourceID, &s.RootEntryID, &s.ConfigurationRevision, &s.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		err = storagesource.ErrSourceUnavailable
	}
	return s, err
}
func (r *NativeStorageRegistry) checkInstallation(ctx context.Context, tx pgx.Tx, id int, owner uuid.UUID, lock bool) (*Installation, error) {
	query := `SELECT ` + installationColumns + ` FROM plugin_installations WHERE id=$1 AND owner_id=$2 AND kind='plugin' AND EXISTS(SELECT 1 FROM bloem_storage_installations n WHERE n.installation_id=$1 AND n.owner_id=$2 AND n.protocol_version=1)`
	if lock {
		query += ` FOR NO KEY UPDATE`
	}
	return scanInstallation(tx.QueryRow(ctx, query, id, owner))
}

// ReplaceConfiguration replaces the complete config, increments both durable
// revisions and fences discovery in one transaction. Lock order is installation
// (NO KEY UPDATE), then source, then runs; scan leases lock only source then runs.
// Leases never acquire installation locks, so they cannot invert this order.
func (r *NativeStorageRegistry) ReplaceConfiguration(ctx context.Context, key, owner uuid.UUID, config map[string]map[string]any) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	s, err := nativeStorageSource(ctx, tx, key, owner, false)
	if err != nil {
		return 0, err
	}
	if s.InstallationID == nil {
		return 0, storagesource.ErrSourceUnavailable
	}
	installation, err := r.checkInstallation(ctx, tx, int(*s.InstallationID), owner, true)
	if err != nil {
		return 0, err
	}
	s, err = nativeStorageSource(ctx, tx, key, owner, true)
	if err != nil {
		return 0, err
	}
	if !s.Enabled || !installation.Enabled || s.InstallationID == nil || int(*s.InstallationID) != installation.ID || s.PluginID != installation.PluginID {
		return 0, storagesource.ErrSourceUnavailable
	}
	if err = r.replaceConfigTx(ctx, tx, installation.ID, config); err != nil {
		return 0, err
	}
	// All sources sharing this installation consume the same config and must fence.
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE installation_id=$1`, installation.ID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET state='failed',lease_epoch=lease_epoch+1 WHERE source_key IN(SELECT key FROM bloem_storage_sources WHERE installation_id=$1) AND state='running'`, installation.ID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE plugin_installations SET runtime_generation=runtime_generation+1,updated_at=now() WHERE id=$1`, installation.ID); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return s.ConfigurationRevision + 1, nil
}

// Disable/Uninstall retain source identity, bindings, entries, catalog references
// and progress, and detach sources after fencing. Callers cancel active runtime
// sessions immediately after success. Uninstall deletes entitlements first.
func (r *NativeStorageRegistry) Disable(ctx context.Context, id int, owner uuid.UUID) error {
	return r.remove(ctx, id, owner, false)
}
func (r *NativeStorageRegistry) Uninstall(ctx context.Context, id int, owner uuid.UUID) error {
	return r.remove(ctx, id, owner, true)
}
func (r *NativeStorageRegistry) remove(ctx context.Context, id int, owner uuid.UUID, uninstall bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = r.checkInstallation(ctx, tx, id, owner, true); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_sources SET enabled=false,configuration_revision=configuration_revision+1 WHERE installation_id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_runs SET state='failed',lease_epoch=lease_epoch+1 WHERE source_key IN(SELECT key FROM bloem_storage_sources WHERE installation_id=$1) AND state='running'`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_storage_sources SET installation_id=NULL WHERE installation_id=$1`, id); err != nil {
		return err
	}
	if uninstall {
		if _, err = tx.Exec(ctx, `DELETE FROM organization_entitlements WHERE plugin_installation_id=$1`, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM plugin_installations WHERE id=$1`, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE plugin_installations SET enabled=false,runtime_generation=runtime_generation+1,updated_at=now() WHERE id=$1`, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// buildBinaryPluginArchive ends in public capability validation; native-only
// packages use its existing ZIP layout and entry writer with private validation.
func buildNativeStorageArchive(manifest, binary []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"plugin", binary}} {
		if err := writeArchiveEntry(writer, entry.name, entry.data); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
func validateNativeStorageArchive(data []byte, manifest *publicv1.PluginManifest, checksum string) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(archive.File) != 2 {
		return errors.New("unexpected native archive members")
	}
	seen := make(map[string]bool)
	for _, file := range archive.File {
		limit := int64(1 << 20)
		if file.Name == "plugin" {
			limit = 256 << 20
		} else if file.Name != "manifest.json" {
			return errors.New("invalid native archive path")
		}
		if seen[file.Name] || file.UncompressedSize64 > uint64(limit) {
			return errors.New("invalid native archive size or duplicate")
		}
		seen[file.Name] = true
		reader, err := file.Open()
		if err != nil {
			return err
		}
		contents, err := io.ReadAll(io.LimitReader(reader, limit+1))
		_ = reader.Close()
		if err != nil {
			return err
		}
		if int64(len(contents)) > limit {
			return errors.New("native archive member exceeds bound")
		}
		if file.Name == "plugin" {
			sum := sha256.Sum256(contents)
			if hex.EncodeToString(sum[:]) != checksum {
				return errors.New("native archive executable checksum mismatch")
			}
		} else {
			var m publicv1.PluginManifest
			if err := protojson.Unmarshal(contents, &m); err != nil {
				return err
			}
			if !proto.Equal(&m, manifest) {
				return errors.New("native archive manifest mismatch")
			}
		}
	}
	return nil
}
