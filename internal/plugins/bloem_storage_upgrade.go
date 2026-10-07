package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
)

// UpgradeAuthorized installs a newer approved artifact of the same plugin on an
// existing native installation. The installation, its source, encrypted
// configuration, library location and catalog are kept: only the executable,
// its archive and the installation's version change, and the runtime
// generation advances so running plugin processes are replaced. authorize runs
// inside the transaction after the installation and source are locked.
func (r *NativeStorageRegistry) UpgradeAuthorized(ctx context.Context, installationID int, key, owner uuid.UUID, artifactKey string, binary []byte, authorize NativeStorageAuthorizeTx) (*NativeStorageSnapshot, error) {
	if authorize == nil {
		return nil, errors.New("native storage upgrade requires authorization")
	}
	return r.upgrade(ctx, installationID, key, owner, artifactKey, binary, authorize)
}

func (r *NativeStorageRegistry) upgrade(ctx context.Context, installationID int, key, owner uuid.UUID, artifactKey string, binary []byte, authorize NativeStorageAuthorizeTx) (*NativeStorageSnapshot, error) {
	operationID := uuid.New()
	a, ok := r.approved[artifactKey]
	if !ok {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	if installationID <= 0 || key == uuid.Nil || owner == uuid.Nil {
		return nil, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	if len(binary) == 0 || len(binary) > 256<<20 {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != a.Checksum {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	if goos, arch, known := nativeManagementBinaryPlatform(binary); !known || goos != a.OS || arch != a.Arch {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	manifestJSON, err := protojson.Marshal(a.Manifest)
	if err != nil {
		return nil, err
	}
	archive, err := buildNativeStorageArchive(manifestJSON, binary)
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
	if err = os.WriteFile(path, binary, 0500); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0400); err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='10s'"); err != nil {
		return nil, err
	}
	// Lock order: installation, then source, as configuration and removal do.
	installation, err := r.checkInstallation(ctx, tx, installationID, owner, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storagesource.ErrSourceUnavailable
	}
	if err != nil {
		return nil, err
	}
	source, err := nativeStorageSource(ctx, tx, key, owner, true)
	if err != nil {
		return nil, err
	}
	if source.InstallationID == nil || *source.InstallationID != int64(installationID) || installation.PluginID != source.PluginID {
		return nil, storagesource.ErrSourceUnavailable
	}
	if a.Manifest.GetPluginId() != installation.PluginID {
		return nil, &catalog.NativeOnboardingError{Code: "artifact_rejected"}
	}
	if err = authorize(ctx, tx); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE plugin_installations SET version=$2, install_path=$3, runtime_generation=runtime_generation+1, updated_at=now() WHERE id=$1`,
		installationID, a.Manifest.GetVersion(), path); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE plugin_archives SET manifest_json=$2, checksum=$3, archive_bytes=$4, updated_at=now() WHERE plugin_installation_id=$1`,
		installationID, manifestJSON, a.Checksum, archive)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, storagesource.ErrSourceUnavailable
	}
	// Once COMMIT is attempted, a transport error cannot prove rollback; keep
	// the package so a committed upgrade stays runnable.
	keep = true
	if err = tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			keep = false
		}
		return nil, nativeManagementCommitError(err, "upgrade", key, operationID)
	}
	return r.Snapshot(ctx, key, owner)
}
