//go:build integration

package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func initialS3Fixture(t *testing.T) (*NativeStorageRegistry, NativeStorageInstallRequest) {
	t.Helper()
	pool := nativeRegistryDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	path, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a := approvedNativeFixture()
	sum := sha256.Sum256(binary)
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum = a.Checksum
	a.Manifest.PluginId = nativeS3PluginID
	a.Manifest.GlobalConfigSchema = []*publicv1.ConfigSchema{{Key: "connection", Required: true, JsonSchema: `{"type":"object","required":["source_id"]}`}}
	r, err := NewNativeStorageRegistry(pool, cipher, nativeS3PrivateRoot(t), map[string]NativeStorageArtifact{"s3": a})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return r, NativeStorageInstallRequest{ArtifactKey: "s3", Binary: binary, Source: storagesource.SourceConfig{Key: uuid.New(), OwnerID: owner, ProviderSourceID: " Requested source ", Enabled: true}, Config: map[string]map[string]any{"connection": {"source_id": " Requested source ", "nested": map[string]any{"secret": "synthetic-original"}}}}
}
func initialGuard(context.Context, pgx.Tx) error { return nil }
func assertInitialRows(t *testing.T, r *NativeStorageRegistry, id int, key uuid.UUID, want bool) {
	t.Helper()
	var present bool
	if err := r.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM plugin_installations WHERE id=$1) OR EXISTS(SELECT 1 FROM bloem_storage_sources WHERE key=$2) OR EXISTS(SELECT 1 FROM plugin_archives WHERE plugin_installation_id=$1) OR EXISTS(SELECT 1 FROM plugin_runtime_configs WHERE plugin_installation_id=$1)`, id, key).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != want {
		t.Fatalf("published rows=%v, want %v", present, want)
	}
}
func TestInitialS3PreparePublishReservedIdentity(t *testing.T) {
	r, req := initialS3Fixture(t)
	p, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.DiscardInitialS3Install(p); err != nil {
			t.Error(err)
		}
	})
	s, err := p.RuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	assertInitialRows(t, r, s.Installation.ID, req.Source.Key, false)
	state := nativeS3SnapshotPath(t, s)
	if filepath.Base(filepath.Dir(state)) != "installation-"+strconv.Itoa(s.Installation.ID) {
		t.Fatal("state does not use final ID")
	}
	req.Config["connection"]["nested"].(map[string]any)["secret"] = "caller-mutated"
	s.Config[0].Value.Fields["source_id"].Kind = nil
	s.Manifest.PluginId = "caller-mutated"
	*s.Source.InstallationID = -1
	s.Source.ProviderSourceID = "caller-mutated"
	s.Installation.Enabled = false
	req.Binary[0] ^= 1
	frozen, err := p.RuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Installation.ID <= 0 || *frozen.Source.InstallationID != int64(frozen.Installation.ID) || frozen.Source.ProviderSourceID != req.Source.ProviderSourceID || !frozen.Installation.Enabled || frozen.Manifest.PluginId != nativeS3PluginID || frozen.Config[0].Value.AsMap()["nested"].(map[string]any)["secret"] != "synthetic-original" {
		t.Fatal("preparation shares caller state")
	}
	if err := os.WriteFile(state, []byte("synthetic durable identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureInitialS3DiscoveryState(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	root := " /opaque:Root%2f/ "
	published, err := r.PublishInitialS3InstallAuthorized(t.Context(), p, root, initialGuard)
	if err != nil {
		t.Fatal(err)
	}
	if published.Installation.ID != frozen.Installation.ID || published.Generation != 1 || published.Source.RootEntryID != root || published.Source.ProviderSourceID != req.Source.ProviderSourceID {
		t.Fatal("publication changed reserved or opaque identity")
	}
	assertInitialRows(t, r, published.Installation.ID, req.Source.Key, true)
	restarted, err := r.Snapshot(t.Context(), req.Source.Key, req.Source.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if nativeS3SnapshotPath(t, restarted) != state {
		t.Fatal("publication rotated state")
	}
	configs, err := r.configs.ListGlobalConfigs(t.Context(), published.Installation.ID)
	if err != nil || len(configs) != 1 {
		t.Fatal("original config missing", err)
	}
	if _, found := configs[0].Value["state_path"]; found {
		t.Fatal("runtime state path persisted")
	}
	if configs[0].Value["nested"].(map[string]any)["secret"] != "synthetic-original" {
		t.Fatal("published caller mutation")
	}
	if _, err := r.PublishInitialS3InstallAuthorized(t.Context(), p, root, initialGuard); err == nil {
		t.Fatal("reused preparation published")
	}
	if err := r.DiscardInitialS3Install(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("discard deleted published state")
	}
}

func TestInitialS3PreparationRefusals(t *testing.T) {
	for _, mode := range []string{"guard", "late-guard", "collision", "foreign", "copy", "fingerprint", "approval", "unsafe-state", "missing-state", "missing-state-file", "state-bytes", "replaced-state", "replaced-state-file", "replaced-package", "replaced-executable", "executable", "explicit-root"} {
		t.Run(mode, func(t *testing.T) {
			r, req := initialS3Fixture(t)
			if mode == "explicit-root" {
				req.Source.RootEntryID = "caller-root"
				if _, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard); err == nil {
					t.Fatal("explicit root accepted by discovery")
				}
				return
			}
			p, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.DiscardInitialS3Install(p) })
			snapshot, err := p.RuntimeSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			state := nativeS3SnapshotPath(t, snapshot)
			if err := os.WriteFile(state, []byte("synthetic stable identity"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureInitialS3DiscoveryState(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			guard := NativeStorageAuthorizeTx(initialGuard)
			target := r
			handle := p
			switch mode {
			case "guard":
				guard = func(context.Context, pgx.Tx) error {
					return &catalog.NativeOnboardingError{Code: "authorization_state_stale"}
				}
			case "late-guard":
				calls := 0
				guard = func(context.Context, pgx.Tx) error {
					calls++
					if calls == 2 {
						return &catalog.NativeOnboardingError{Code: "authorization_state_stale"}
					}
					return nil
				}
			case "collision":
				if _, err := r.pool.Exec(t.Context(), `INSERT INTO bloem_storage_sources(key,owner_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled) VALUES($1,$2,$3,'retained','retained',1,false)`, req.Source.Key, req.Source.OwnerID, nativeS3PluginID); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				target, _ = NewNativeStorageRegistry(r.pool, r.configs.cipher, r.baseDir, r.approved)
			case "copy":
				copy := *p
				handle = &copy
			case "fingerprint":
				p.source.ProviderSourceID = "tampered"
			case "approval":
				delete(r.approved, "s3")
			case "unsafe-state":
				if err := os.Chmod(filepath.Dir(state), 0755); err != nil {
					t.Fatal(err)
				}
			case "missing-state":
				if err := os.RemoveAll(filepath.Dir(state)); err != nil {
					t.Fatal(err)
				}
			case "missing-state-file":
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
			case "state-bytes":
				if err := os.WriteFile(state, bytes.Repeat([]byte("x"), int(p.stateFile.Size())), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(state, time.Now(), p.stateFile.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "replaced-state":
				if err := os.Rename(filepath.Dir(state), filepath.Join(r.baseDir, "old-state")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Dir(state), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(state, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced-state-file":
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(state, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced-package":
				dir := filepath.Dir(snapshot.Installation.InstallPath)
				if err := os.Rename(dir, filepath.Join(r.baseDir, "old-package")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Installation.InstallPath, req.Binary, 0500); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), p.manifestJSON, 0400); err != nil {
					t.Fatal(err)
				}
			case "replaced-executable":
				if err := os.Remove(snapshot.Installation.InstallPath); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Installation.InstallPath, req.Binary, 0500); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Chmod(snapshot.Installation.InstallPath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Installation.InstallPath, []byte("tampered"), 0500); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := target.PublishInitialS3InstallAuthorized(t.Context(), handle, "opaque-root", guard); err == nil {
				t.Fatal("invalid preparation published")
			}
			var exists bool
			if err := r.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM plugin_installations WHERE id=$1)`, snapshot.Installation.ID).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Fatal("refusal published installation")
			}
			if strings.HasPrefix(mode, "replaced-") {
				if err := r.DiscardInitialS3Install(p); err == nil {
					t.Fatal("replacement cleanup was not refused")
				}
				replacement := state
				if mode == "replaced-package" || mode == "replaced-executable" {
					replacement = snapshot.Installation.InstallPath
				}
				if _, err := os.Stat(replacement); err != nil {
					t.Fatal("cleanup deleted a replacement", err)
				}
			}
		})
	}
}

func TestInitialS3CommitOutcomeOwnsFiles(t *testing.T) {
	for _, mode := range []string{"ack-lost", "unknown-before-commit", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			r, req := initialS3Fixture(t)
			p, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard)
			if err != nil {
				t.Fatal(err)
			}
			s, err := p.RuntimeSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			state := nativeS3SnapshotPath(t, s)
			if err := os.WriteFile(state, []byte("identity"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureInitialS3DiscoveryState(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			r.commitInstall = func(ctx context.Context, tx pgx.Tx) error {
				switch mode {
				case "ack-lost":
					if err := tx.Commit(ctx); err != nil {
						return err
					}
					return errors.New("synthetic lost acknowledgement")
				case "rollback":
					_, _ = tx.Exec(ctx, `SELECT 1/0`)
					return tx.Commit(ctx)
				default:
					return errors.New("synthetic uncertain transport")
				}
			}
			_, err = r.PublishInitialS3InstallAuthorized(t.Context(), p, "root", initialGuard)
			if err == nil {
				t.Fatal("faulted commit accepted")
			}
			var unknown *catalog.MutationOutcomeUnknown
			if errors.As(err, &unknown) != (mode != "rollback") {
				t.Fatal("commit outcome classified incorrectly", err)
			}
			if unknown != nil && (unknown.Operation != "install" || unknown.SourceKey == nil || *unknown.SourceKey != req.Source.Key || unknown.OperationID == uuid.Nil) {
				t.Fatal("missing reconciliation identity")
			}
			if err := r.DiscardInitialS3Install(p); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{state, s.Installation.InstallPath} {
				_, statErr := os.Stat(path)
				if mode == "rollback" {
					if !errors.Is(statErr, os.ErrNotExist) {
						t.Fatal("positive rollback retained files", statErr)
					}
				} else if statErr != nil {
					t.Fatal("unknown outcome lost runnable state", statErr)
				}
			}
			assertInitialRows(t, r, s.Installation.ID, req.Source.Key, mode == "ack-lost")
		})
	}
}

func TestInitialS3PublishRechecksExpiryAfterFinalVerification(t *testing.T) {
	for _, mode := range []string{"admin-context", "login-session"} {
		t.Run(mode, func(t *testing.T) {
			r, req := initialS3Fixture(t)
			p, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.DiscardInitialS3Install(p) })
			snapshot, err := p.RuntimeSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			state := nativeS3SnapshotPath(t, snapshot)
			if err := os.WriteFile(state, []byte("synthetic stable identity"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureInitialS3DiscoveryState(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			var account int
			session := uuid.NewString()
			claimsExpiry := time.Now().Add(time.Hour)
			if err := r.pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('expiry-admin','expiry@example.test','synthetic','admin',true) RETURNING id`).Scan(&account); err != nil {
				t.Fatal(err)
			}
			if _, err := r.pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic final expiry',now()+interval '1 hour')`, session, account); err != nil {
				t.Fatal(err)
			}
			checks := 0
			guard := func(ctx context.Context, tx pgx.Tx) error {
				var sessionExpiry, now time.Time
				if err := tx.QueryRow(ctx, `SELECT expires_at,clock_timestamp() FROM auth_sessions WHERE id=$1 FOR SHARE`, session).Scan(&sessionExpiry, &now); err != nil {
					return err
				}
				if !claimsExpiry.After(now) || !sessionExpiry.After(now) {
					return initialS3Error("authorization_state_stale")
				}
				checks++
				if checks == 2 {
					// Cross the expiry boundary immediately after the retained
					// row-wait check, before final file hashing and snapshot work.
					// Advancing the deadline is deterministic and needs no sleep.
					switch mode {
					case "admin-context":
						claimsExpiry = time.Now().Add(-time.Hour)
					case "login-session":
						if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, session); err != nil {
							return err
						}
					}
				}
				return nil
			}
			_, err = r.PublishInitialS3InstallAuthorized(t.Context(), p, "root", guard)
			var refusal *catalog.NativeOnboardingError
			if !errors.As(err, &refusal) || refusal.Code != "authorization_state_stale" {
				t.Errorf("publication missed final %s expiry: %v", mode, err)
			}
			assertInitialRows(t, r, snapshot.Installation.ID, req.Source.Key, false)
			if err := r.DiscardInitialS3Install(p); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{state, snapshot.Installation.InstallPath} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("expired publication retained unpublished files", err)
				}
			}
		})
	}
}

func TestInitialS3PublishRechecksAfterRowWait(t *testing.T) {
	for _, mode := range []string{"package-directory", "executable", "state-bytes"} {
		t.Run(mode, func(t *testing.T) {
			r, req := initialS3Fixture(t)
			p, err := r.PrepareInitialS3InstallAuthorized(t.Context(), req, initialGuard)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.DiscardInitialS3Install(p) })
			snapshot, err := p.RuntimeSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			state := nativeS3SnapshotPath(t, snapshot)
			if err := os.WriteFile(state, []byte("synthetic stable identity"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureInitialS3DiscoveryState(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			blocker, err := r.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			if _, err := blocker.Exec(t.Context(), `INSERT INTO plugin_installations(id,plugin_id,version,install_path,enabled,owner_id) VALUES($1,'synthetic.blocker','1','/synthetic',false,$2)`, snapshot.Installation.ID, req.Source.OwnerID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, err := r.PublishInitialS3InstallAuthorized(t.Context(), p, "root", initialGuard)
				result <- err
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waiting:
			for {
				select {
				case <-poll.C:
					var waiting bool
					if err := r.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'INSERT INTO plugin_installations(id,%')`).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break waiting
					}
				case err := <-result:
					t.Fatalf("publication ended before retained row wait: %v", err)
				case <-deadline.C:
					t.Fatal("publication never reached its row wait")
				}
			}
			switch mode {
			case "package-directory":
				dir := filepath.Dir(snapshot.Installation.InstallPath)
				if err := os.Rename(dir, filepath.Join(r.baseDir, "old-package")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Installation.InstallPath, req.Binary, 0500); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), p.manifestJSON, 0400); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.Rename(snapshot.Installation.InstallPath, snapshot.Installation.InstallPath+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(snapshot.Installation.InstallPath, req.Binary, 0500); err != nil {
					t.Fatal(err)
				}
			case "state-bytes":
				if err := os.WriteFile(state, bytes.Repeat([]byte("x"), int(p.stateFile.Size())), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(state, time.Now(), p.stateFile.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if err := blocker.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("publication missed filesystem change during row wait")
				}
			case <-deadline.C:
				t.Fatal("publication did not finish after wait released")
			}
			assertInitialRows(t, r, snapshot.Installation.ID, req.Source.Key, false)
			if mode != "state-bytes" {
				if err := r.DiscardInitialS3Install(p); err == nil {
					t.Fatal("replacement cleanup not refused")
				}
				if _, err := os.Stat(snapshot.Installation.InstallPath); err != nil {
					t.Fatal("cleanup removed a replacement", err)
				}
			}
		})
	}
}
