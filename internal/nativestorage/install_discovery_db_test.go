//go:build integration

package nativestorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const installFixtureSchema = `{"type":"object","required":["source_id"],"properties":{"source_id":{"type":"string"}}}`
const installFixtureRoot = " /opaque:S3-root%2f/ "

func discoveryDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	template := config.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-disposable discovery database")
	}
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	name := "bloem_storage_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.Database = name
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return pool
}
func discoveryPrivateRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := os.MkdirTemp(home, ".bloem-discovery-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}
func discoveryFixture(t *testing.T, binary []byte, mode string, enabled bool) (*SourceManagement, auth.AdminContextClaims, DiscoverInstallCommand) {
	t.Helper()
	pool := discoveryDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.storage.s3", Version: "0.1.0", Checksum: checksum, SiloApiVersion: "v1", SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "connection", Required: true, JsonSchema: installFixtureSchema}}}
	root := discoveryPrivateRoot(t)
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, root, map[string]plugins.NativeStorageArtifact{"s3": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.AdminContextClaims{Scope: auth.AdminScopePlatform, SessionID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username,email,password_hash,role,enabled) VALUES('discovery-admin','discovery@example.test','synthetic','admin',true) RETURNING id,account_incarnation_id`).Scan(&actor.AccountID, &actor.AccountIncarnationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic discovery fixture',now()+interval '1 hour')`, actor.SessionID, actor.AccountID); err != nil {
		t.Fatal(err)
	}
	host := &Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{})}
	management := NewSourceManagement(pool, registry)
	management.SetInstallDiscoveryHost(host)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return management, actor, DiscoverInstallCommand{ArtifactKey: "s3", ProviderSourceID: " Requested source ", Enabled: enabled, Binary: binary, Config: map[string]map[string]any{"connection": {"source_id": " Requested source ", "mode": mode, "notify": filepath.Join(root, "notify"), "release": filepath.Join(root, "release")}}}
}
func discoveryOrganization(t *testing.T, pool *pgxpool.Pool, account int) (uuid.UUID, uuid.UUID, int64, int64) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var org, membership uuid.UUID
	var policy, security int64
	if err := tx.QueryRow(t.Context(), `INSERT INTO organizations(slug,name,status,owner_account_id) VALUES('discovery-org','Synthetic organization','active',$1) RETURNING id,policy_revision`, account).Scan(&org, &policy); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(t.Context(), `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active','admin') ON CONFLICT(organization_id,account_id) DO UPDATE SET status='active',legacy_role='admin' RETURNING id,security_revision`, org, account).Scan(&membership, &security); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return org, membership, policy, security
}
func discoveryMembershipRevoke(t *testing.T, pool *pgxpool.Pool, membership uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE organization_memberships SET status='suspended' WHERE id=$1`, membership); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func waitDiscoveryNotification(t *testing.T, path string, result <-chan error) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil {
				parts := strings.SplitN(string(data), "\n", 2)
				if len(parts) != 2 {
					continue
				}
				pid, err := strconv.Atoi(parts[0])
				if err != nil {
					t.Fatal(err)
				}
				return pid, parts[1]
			}
		case err := <-result:
			t.Fatalf("discovery ended before gate: %v", err)
		case <-ctx.Done():
			t.Fatal("discovery did not reach observable gate")
		}
	}
}
func assertDiscoveryEmpty(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("discovery published before admission", count)
	}
}
func assertDiscoveryReaped(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("discovery process still exists", err)
	}
}

// Hold the real ID reservation after preparation's authority transaction has
// ended. Revocation must prevent even process startup when preparation resumes.
func TestInitialS3DiscoveryRechecksBeforeStartup(t *testing.T) {
	buildCtx, buildCancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer buildCancel()
	path := filepath.Join(t.TempDir(), "s3-fixture")
	build := exec.CommandContext(buildCtx, "go", "build", "-ldflags=-s -w", "-o", path, "./testdata/s3installfixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build discovery fixture: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"account", "session", "membership"} {
		t.Run(mode, func(t *testing.T) {
			management, actor, cmd := discoveryFixture(t, binary, "block-configure", true)
			if mode == "membership" {
				org, membership, policy, security := discoveryOrganization(t, management.pool, actor.AccountID)
				actor.Scope, actor.OrganizationID, actor.MembershipID = auth.AdminScopeOrganization, org, membership
				actor.PolicyRevision, actor.SecurityRevision, actor.EffectiveAuthority = policy, security, "organization_admin"
				if _, err := management.pool.Exec(t.Context(), `UPDATE users SET role='user' WHERE id=$1`, actor.AccountID); err != nil {
					t.Fatal(err)
				}
				if err := management.pool.QueryRow(t.Context(), `SELECT security_revision FROM organization_memberships WHERE id=$1`, membership).Scan(&actor.SecurityRevision); err != nil {
					t.Fatal(err)
				}
				actor.SessionID = uuid.NewString()
				if _, err := management.pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic preparation authority',now()+interval '1 hour')`, actor.SessionID, actor.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			// The blocked reservation owns the application's sole connection.
			sideConfig := management.pool.Config().Copy()
			sideConfig.MaxConns = 3
			side, err := pgxpool.NewWithConfig(t.Context(), sideConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer side.Close()
			blocker, err := side.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			var sequence string
			if err := blocker.QueryRow(t.Context(), `SELECT pg_get_serial_sequence('plugin_installations','id')`).Scan(&sequence); err != nil {
				t.Fatal(err)
			}
			if _, err := blocker.Exec(t.Context(), "ALTER SEQUENCE "+pgx.Identifier(strings.Split(sequence, ".")).Sanitize()+" CACHE 1"); err != nil {
				t.Fatal(err)
			}
			request, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := management.DiscoverInstall(request, actor, cmd); result <- err }()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waiting:
			for {
				select {
				case <-poll.C:
					var waiting bool
					if err := side.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT nextval(pg_get_serial_sequence(%')`).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break waiting
					}
				case err := <-result:
					t.Fatalf("preparation ended before reservation gate: %v", err)
				case <-deadline.C:
					t.Fatal("preparation never reached reservation gate")
				}
			}
			switch mode {
			case "account":
				if _, err := side.Exec(t.Context(), `UPDATE users SET enabled=false WHERE id=$1`, actor.AccountID); err != nil {
					t.Fatal(err)
				}
			case "session":
				if _, err := side.Exec(t.Context(), `UPDATE auth_sessions SET revoked_at=now() WHERE id=$1`, actor.SessionID); err != nil {
					t.Fatal(err)
				}
			case "membership":
				discoveryMembershipRevoke(t, side, actor.MembershipID)
			}
			if err := blocker.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(cmd.Config["connection"]["notify"].(string))
			completion := time.NewTimer(60 * time.Second)
			defer completion.Stop()
			for {
				select {
				case <-poll.C:
					if _, err := os.Stat(filepath.Join(root, "startup")); err == nil {
						cancel()
						select {
						case <-result:
						case <-completion.C:
							t.Fatal("revoked discovery process failed to reap")
						}
						assertDiscoveryEmpty(t, management.pool)
						t.Fatal("revoked preparation launched provider process")
					}
				case err := <-result:
					var refusal *catalog.NativeOnboardingError
					if !errors.As(err, &refusal) || refusal.Code != "authorization_state_stale" {
						t.Fatal("startup authority refusal not mapped", err)
					}
					for _, marker := range []string{"startup", "notify"} {
						if _, err := os.Stat(filepath.Join(root, marker)); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("revoked preparation ran provider", marker, err)
						}
					}
					assertDiscoveryEmpty(t, management.pool)
					return
				case <-completion.C:
					t.Fatal("discovery failed to finish after preparation revocation")
				}
			}
		})
	}
}

func TestInitialS3DiscoverInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "s3-fixture")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags=-s -w", "-o", path, "./testdata/s3installfixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build discovery fixture: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"fresh-root", "disabled", "empty", "unpinned", "malformed", "duplicate", "missing", "platform-organization", "organization-admin", "revoke-membership", "revoke-owner", "revoke-session", "revoke-admin", "cancel-configure", "cancel-startup", "cancel-fresh", "gate-success", "drain-host"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := mode
			if mode == "disabled" {
				fixtureMode = "fresh-root"
			}
			if strings.HasPrefix(mode, "revoke") || mode == "cancel-fresh" || mode == "gate-success" || mode == "drain-host" {
				fixtureMode = "gate-fresh"
			}
			if mode == "cancel-configure" {
				fixtureMode = "block-configure"
			}
			if mode == "cancel-startup" {
				fixtureMode = "block-startup"
			}
			management, actor, cmd := discoveryFixture(t, binary, fixtureMode, mode != "disabled")
			var organizationID *uuid.UUID
			if mode == "platform-organization" || mode == "organization-admin" || mode == "revoke-membership" || mode == "revoke-owner" {
				org, membership, policy, security := discoveryOrganization(t, management.pool, actor.AccountID)
				organizationID = &org
				if mode == "platform-organization" {
					cmd.OrganizationID = &org
				} else {
					actor.Scope = auth.AdminScopeOrganization
					actor.OrganizationID = org
					actor.MembershipID = membership
					actor.PolicyRevision = policy
					actor.SecurityRevision = security
					actor.EffectiveAuthority = "organization_admin"
					if _, err := management.pool.Exec(t.Context(), `UPDATE users SET role='user' WHERE id=$1`, actor.AccountID); err != nil {
						t.Fatal(err)
					}
					if err := management.pool.QueryRow(t.Context(), `SELECT security_revision FROM organization_memberships WHERE id=$1`, actor.MembershipID).Scan(&actor.SecurityRevision); err != nil {
						t.Fatal(err)
					}
					actor.SessionID = uuid.NewString()
					if _, err := management.pool.Exec(t.Context(), `INSERT INTO auth_sessions(id,user_id,device_name,expires_at) VALUES($1,$2,'Synthetic organization admin',now()+interval '1 hour')`, actor.SessionID, actor.AccountID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !management.InitialRootDiscoveryReady() {
				t.Fatal("wired discovery unavailable")
			}
			request, stop := context.WithCancel(t.Context())
			defer stop()
			var view SourceView
			result := make(chan error, 1)
			go func() { var err error; view, err = management.DiscoverInstall(request, actor, cmd); result <- err }()
			gated := strings.HasPrefix(mode, "revoke") || strings.HasPrefix(mode, "cancel") || mode == "gate-success" || mode == "drain-host"
			var pid int
			var state string
			if gated {
				pid, state = waitDiscoveryNotification(t, cmd.Config["connection"]["notify"].(string), result)
				query, cancel := context.WithTimeout(t.Context(), time.Second)
				var one int
				err := management.pool.QueryRow(query, `SELECT 1`).Scan(&one)
				cancel()
				if err != nil || one != 1 {
					t.Fatal("provider I/O retained the only pool connection", err)
				}
				assertDiscoveryEmpty(t, management.pool)
				management.installDiscoveryHost.mu.Lock()
				admitted := management.installDiscoveryHost.readers
				management.installDiscoveryHost.mu.Unlock()
				if admitted != 1 {
					t.Fatal("discovery did not retain host admission", admitted)
				}
				if mode == "drain-host" {
					drain, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
					err := management.installDiscoveryHost.Shutdown(drain)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("host shutdown did not retain discovery admission", err)
					}
					if release, err := management.installDiscoveryHost.AcquireOpen(); !errors.Is(err, storageplugin.ErrClosed) {
						if release != nil {
							release()
						}
						t.Fatal("shutdown allowed new admission", err)
					}
				}
				switch mode {
				case "revoke-membership":
					discoveryMembershipRevoke(t, management.pool, actor.MembershipID)
				case "revoke-owner":
					if _, err := management.pool.Exec(t.Context(), `UPDATE resource_owners SET revision=revision+1 WHERE organization_id=$1`, actor.OrganizationID); err != nil {
						t.Fatal(err)
					}
				case "revoke-session":
					if _, err := management.pool.Exec(t.Context(), `UPDATE auth_sessions SET revoked_at=now() WHERE id=$1`, actor.SessionID); err != nil {
						t.Fatal(err)
					}
				case "revoke-admin":
					if _, err := management.pool.Exec(t.Context(), `UPDATE users SET role='user' WHERE id=$1`, actor.AccountID); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(mode, "cancel") {
					stop()
				} else if err := os.WriteFile(cmd.Config["connection"]["release"].(string), []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				success := mode == "fresh-root" || mode == "disabled" || mode == "gate-success" || mode == "platform-organization" || mode == "organization-admin" || mode == "drain-host"
				if success {
					if err != nil {
						t.Fatal(err)
					}
					if view.RootEntryID != installFixtureRoot || view.ProviderSourceID != cmd.ProviderSourceID || view.ConfigurationRevision != 1 || view.Enabled != (mode != "disabled") || view.InstallationID == nil {
						t.Fatal("wrong discovered publication", view)
					}
					if mode == "disabled" && view.State != "disabled" {
						t.Fatal("disabled discovery enabled installation")
					}
					if organizationID != nil {
						if view.OrganizationID == nil || *view.OrganizationID != *organizationID || view.OwnerKind != "organization" {
							t.Fatal("organization discovery published wrong owner", view)
						}
					} else if view.OrganizationID != nil || view.OwnerKind != "platform" {
						t.Fatal("platform discovery published wrong owner", view)
					}
					var owner uuid.UUID
					if organizationID != nil {
						if err := management.pool.QueryRow(t.Context(), `SELECT id FROM resource_owners WHERE organization_id=$1`, *organizationID).Scan(&owner); err != nil {
							t.Fatal(err)
						}
					} else if err := management.pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
						t.Fatal(err)
					}
					if mode != "disabled" && mode != "drain-host" {
						snapshot, err := management.registry.Snapshot(t.Context(), view.SourceKey, owner)
						if err != nil {
							t.Fatal(err)
						}
						runtimeState := snapshot.Config[0].Value.Fields["state_path"].GetStringValue()
						if state != "" && state != runtimeState {
							t.Fatal("state changed at publication")
						}
						session, err := management.installDiscoveryHost.Manager.Ensure(t.Context(), storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: 1, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
						if err != nil {
							t.Fatal("generation 1 poisoned or identity state changed", err)
						}
						if session.Context().Err() != nil {
							t.Fatal("resident runtime revoked by discovery cleanup")
						}
						description, err := storageplugin.Describe(t.Context(), session.Provider())
						if err != nil {
							t.Fatal(err)
						}
						if err := storageplugin.ValidateSourceDescription(description, cmd.ProviderSourceID, view.RootEntryID); err != nil {
							t.Fatal("reopened runtime lost root/identity", err)
						}
					}
				} else {
					if err == nil {
						t.Fatal("invalid or canceled discovery published")
					}
					assertDiscoveryEmpty(t, management.pool)
					if strings.HasPrefix(mode, "revoke") {
						var refusal *catalog.NativeOnboardingError
						if !errors.As(err, &refusal) || refusal.Code != "authorization_state_stale" {
							t.Fatal("revocation error not mapped", err)
						}
					}
					if state != "" {
						if _, err := os.Stat(filepath.Dir(state)); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("reaped unpublished state not cleaned", err)
						}
					}
				}
				if gated {
					assertDiscoveryReaped(t, pid)
				}
				if mode == "drain-host" {
					drain, cancel := context.WithTimeout(t.Context(), time.Second)
					defer cancel()
					if err := management.installDiscoveryHost.Shutdown(drain); err != nil {
						t.Fatal("host admission not released after discovery", err)
					}
				}
			case <-time.After(60 * time.Second):
				t.Fatal("discovery failed to finish and reap")
			}
		})
	}
}
