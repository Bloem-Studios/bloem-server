//go:build integration

package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Credential rotation against the real store. These tests only ever compare
// credentials through short fingerprints or booleans, so a failure never prints
// a secret value.

const (
	rotationBookwarehousePluginID = "bloem.storage.bookwarehouse"
	rotationFolderID              = 91941
)

var rotationCredentialKeys = []string{"access_key", "secret_key", "session_token"}

func rotationAllow(context.Context, pgx.Tx) error { return nil }

func rotationFP(v any) string {
	s, _ := v.(string)
	if s == "" {
		return "absent"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func rotationCode(err error) string {
	var refusal *catalog.NativeOnboardingError
	switch {
	case err == nil:
		return "none"
	case errors.As(err, &refusal):
		return refusal.Code
	case errors.Is(err, storagesource.ErrSourceUnavailable):
		return "source_unavailable"
	default:
		return "other"
	}
}

type rotationFixture struct {
	r              *NativeStorageRegistry
	key, owner     uuid.UUID
	installationID int
}

type rotationState struct {
	revision   int64
	generation int
	conn       map[string]any
}

func rotationSeed(pluginID string) map[string]any {
	if pluginID == rotationBookwarehousePluginID {
		return map[string]any{"base_url": "https://books.synthetic.example", "api_key": "synthetic-old-api-key", "source_id": "books", "source_name": "Bookwarehouse"}
	}
	return map[string]any{
		"source_id": "synthetic-source", "source_name": "Synthetic media", "bucket": "synthetic-bucket", "region": "eu-test-1",
		"endpoint": "https://s3.synthetic.example", "prefix": "media/", "revision_mode": "etag", "probe_key": "probe.txt",
		"allow_loopback_http": false, "path_style": true, "max_object_bytes": 4096,
		"access_key": "synthetic-old-access", "secret_key": "synthetic-old-secret", "session_token": "synthetic-old-token",
	}
}

func newRotationFixture(t *testing.T, pluginID string, enabled bool) *rotationFixture {
	t.Helper()
	pool := nativeRegistryDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	binaryPath, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	a := approvedNativeFixture()
	sum := sha256.Sum256(binary)
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum = a.Checksum
	a.Manifest.PluginId = pluginID
	a.Manifest.GlobalConfigSchema = []*publicv1.ConfigSchema{{Key: "connection", JsonSchema: `{"type":"object"}`}}
	r, err := NewNativeStorageRegistry(pool, cipher, nativeS3PrivateRoot(t), map[string]NativeStorageArtifact{"plugin": a})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	installed, err := r.Install(t.Context(), NativeStorageInstallRequest{ArtifactKey: "plugin", Binary: binary,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: enabled},
		Config: map[string]map[string]any{"connection": rotationSeed(pluginID)}})
	if err != nil {
		t.Fatal(err)
	}
	return &rotationFixture{r: r, key: installed.Source.Key, owner: installed.Source.OwnerID, installationID: installed.Installation.ID}
}

// bindLibrary attaches the source to a library, the usual reason a namespace is
// non-empty.
func (f *rotationFixture) bindLibrary(t *testing.T) {
	t.Helper()
	if _, err := f.r.pool.Exec(t.Context(), `INSERT INTO media_folders(id,type,name) VALUES($1,'ebooks','synthetic rotation library')`, rotationFolderID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.pool.Exec(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, uuid.New(), f.key, rotationFolderID); err != nil {
		t.Fatal(err)
	}
}

// addEntry records one discovered entry, the other reason a namespace is non-empty.
func (f *rotationFixture) addEntry(t *testing.T) {
	t.Helper()
	run := uuid.New()
	if _, err := f.r.pool.Exec(t.Context(), `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES($1,$2,1,'complete',1,'fixture',now())`, run, f.key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.pool.Exec(t.Context(), `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES($1,'synthetic-entry','Book.epub','Book.epub',1,10,0,'v1',1,$2)`, f.key, run); err != nil {
		t.Fatal(err)
	}
}

func (f *rotationFixture) state(t *testing.T) rotationState {
	t.Helper()
	source, err := storagesource.NewRepository(f.r.pool).Source(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}
	var generation int
	if err = f.r.pool.QueryRow(t.Context(), `SELECT runtime_generation FROM plugin_installations WHERE id=$1`, f.installationID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	conn, err := f.r.ConnectionConfig(t.Context(), f.installationID)
	if err != nil || conn == nil {
		t.Fatal("stored connection unreadable")
	}
	return rotationState{revision: source.ConfigurationRevision, generation: generation, conn: conn}
}

func (f *rotationFixture) rotate(creds S3Credentials, expected int64) (int64, error) {
	return f.r.RotateCredentials(context.Background(), f.key, f.owner, creds, expected, rotationAllow)
}

// fieldsDiffering lists the keys whose values differ, by key name only.
func rotationFieldsDiffering(before, after map[string]any) []string {
	seen := map[string]bool{}
	for k := range before {
		seen[k] = true
	}
	for k := range after {
		seen[k] = true
	}
	var differing []string
	for k := range seen {
		if !reflect.DeepEqual(before[k], after[k]) {
			differing = append(differing, k)
		}
	}
	sort.Strings(differing)
	return differing
}

func rotationNonCredential(keys []string) []string {
	var out []string
	for _, k := range keys {
		credential := false
		for _, c := range rotationCredentialKeys {
			credential = credential || k == c
		}
		if !credential {
			out = append(out, k)
		}
	}
	return out
}

// requireUnchanged proves a refused call changed nothing the store can see.
func (f *rotationFixture) requireUnchanged(t *testing.T, before rotationState) {
	t.Helper()
	after := f.state(t)
	if after.revision != before.revision || after.generation != before.generation {
		t.Fatalf("refused call moved revision/generation: revision %d->%d generation %d->%d", before.revision, after.revision, before.generation, after.generation)
	}
	if diff := rotationFieldsDiffering(before.conn, after.conn); len(diff) != 0 {
		t.Fatalf("refused call changed stored connection fields %v", diff)
	}
}

func TestRotateCredentialsKeepsOtherFieldsDB(t *testing.T) {
	newCreds := S3Credentials{AccessKey: "synthetic-new-access", SecretKey: "synthetic-new-secret", SessionToken: "synthetic-new-token"}

	t.Run("succeeds on an in-use source and changes only the credentials", func(t *testing.T) {
		for _, tc := range []struct {
			name          string
			binding, rows bool
		}{{"library binding", true, false}, {"discovered entries", false, true}, {"binding and entries", true, true}} {
			t.Run(tc.name, func(t *testing.T) {
				f := newRotationFixture(t, nativeS3PluginID, true)
				if tc.binding {
					f.bindLibrary(t)
				}
				if tc.rows {
					f.addEntry(t)
				}
				before := f.state(t)
				if before.revision != 1 || before.generation != 1 {
					t.Fatalf("unexpected seed state revision=%d generation=%d", before.revision, before.generation)
				}
				// Precondition: the namespace really is non-empty, so a plain replacement refuses.
				_, err := f.r.ReplaceConfigurationAuthorized(t.Context(), f.key, f.owner, before.revision, map[string]map[string]any{"connection": before.conn}, rotationAllow)
				if got := rotationCode(err); got != "configuration_namespace_unverified" {
					t.Fatalf("fixture namespace is not populated: plain replacement code=%s", got)
				}

				revision, err := f.rotate(newCreds, before.revision)
				if err != nil {
					t.Fatalf("rotation of an in-use source refused: code=%s", rotationCode(err))
				}
				after := f.state(t)
				if revision != before.revision+1 || after.revision != before.revision+1 {
					t.Fatalf("revision not incremented by exactly 1: returned=%d stored=%d before=%d", revision, after.revision, before.revision)
				}
				if after.generation != before.generation+1 {
					t.Fatalf("runtime generation not incremented by exactly 1: %d->%d", before.generation, after.generation)
				}
				diff := rotationFieldsDiffering(before.conn, after.conn)
				if other := rotationNonCredential(diff); len(other) != 0 {
					t.Fatalf("rotation changed non-credential fields %v", other)
				}
				if len(after.conn) != len(before.conn) {
					t.Fatalf("stored connection key count changed: %d->%d", len(before.conn), len(after.conn))
				}
				for field, want := range map[string]string{"access_key": newCreds.AccessKey, "secret_key": newCreds.SecretKey, "session_token": newCreds.SessionToken} {
					if rotationFP(after.conn[field]) != rotationFP(want) {
						t.Fatalf("%s was not set to the supplied value", field)
					}
					if rotationFP(after.conn[field]) == rotationFP(before.conn[field]) {
						t.Fatalf("%s did not change", field)
					}
				}
				// The namespace is untouched by a rotation.
				var locations, entries int
				if err = f.r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM library_storage_locations WHERE source_key=$1),(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1)`, f.key).Scan(&locations, &entries); err != nil {
					t.Fatal(err)
				}
				if (locations == 1) != tc.binding || (entries == 1) != tc.rows {
					t.Fatalf("rotation disturbed the namespace: locations=%d entries=%d", locations, entries)
				}
			})
		}
	})

	t.Run("an empty session token clears the stored token", func(t *testing.T) {
		f := newRotationFixture(t, nativeS3PluginID, true)
		f.bindLibrary(t)
		before := f.state(t)
		if rotationFP(before.conn["session_token"]) == "absent" {
			t.Fatal("seed has no session token")
		}
		if _, err := f.rotate(S3Credentials{AccessKey: newCreds.AccessKey, SecretKey: newCreds.SecretKey}, before.revision); err != nil {
			t.Fatalf("rotation refused: code=%s", rotationCode(err))
		}
		after := f.state(t)
		if _, present := after.conn["session_token"]; present {
			t.Fatal("stale session token survived rotation")
		}
		if other := rotationNonCredential(rotationFieldsDiffering(before.conn, after.conn)); len(other) != 0 {
			t.Fatalf("rotation changed non-credential fields %v", other)
		}
		if after.revision != before.revision+1 {
			t.Fatalf("revision %d->%d", before.revision, after.revision)
		}
	})

	t.Run("plain replacement of the same in-use source is still refused", func(t *testing.T) {
		f := newRotationFixture(t, nativeS3PluginID, true)
		f.bindLibrary(t)
		before := f.state(t)
		replacement := map[string]any{}
		for k, v := range before.conn {
			replacement[k] = v
		}
		replacement["access_key"], replacement["secret_key"] = newCreds.AccessKey, newCreds.SecretKey
		_, err := f.r.ReplaceConfigurationAuthorized(t.Context(), f.key, f.owner, before.revision, map[string]map[string]any{"connection": replacement}, rotationAllow)
		if got := rotationCode(err); got != "configuration_namespace_unverified" {
			t.Fatalf("plain replacement code=%s, want configuration_namespace_unverified", got)
		}
		f.requireUnchanged(t, before)
		// Refusing a plain replacement does not disable the rotation route.
		if _, err = f.rotate(newCreds, before.revision); err != nil {
			t.Fatalf("rotation refused after a refused replacement: code=%s", rotationCode(err))
		}
	})

	t.Run("control: plain replacement of an empty-namespace source succeeds", func(t *testing.T) {
		f := newRotationFixture(t, nativeS3PluginID, true)
		before := f.state(t)
		revision, err := f.r.ReplaceConfigurationAuthorized(t.Context(), f.key, f.owner, before.revision, map[string]map[string]any{"connection": before.conn}, rotationAllow)
		if err != nil || revision != before.revision+1 {
			t.Fatalf("empty-namespace replacement: code=%s revision=%d", rotationCode(err), revision)
		}
	})

	t.Run("a non-S3 source is refused and unchanged", func(t *testing.T) {
		for _, inUse := range []bool{false, true} {
			name := map[bool]string{false: "empty namespace", true: "in use"}[inUse]
			t.Run(name, func(t *testing.T) {
				f := newRotationFixture(t, rotationBookwarehousePluginID, true)
				if inUse {
					f.bindLibrary(t)
				}
				before := f.state(t)
				_, err := f.rotate(newCreds, before.revision)
				if !errors.Is(err, storagesource.ErrSourceUnavailable) {
					t.Fatalf("Bookwarehouse rotation code=%s, want source_unavailable", rotationCode(err))
				}
				f.requireUnchanged(t, before)
				for _, field := range rotationCredentialKeys {
					if _, planted := f.state(t).conn[field]; planted {
						t.Fatalf("refused rotation planted %s into a Bookwarehouse config", field)
					}
				}
			})
		}
	})

	t.Run("a disabled source is refused and unchanged", func(t *testing.T) {
		t.Run("installed disabled", func(t *testing.T) {
			f := newRotationFixture(t, nativeS3PluginID, false)
			f.bindLibrary(t)
			before := f.state(t)
			_, err := f.rotate(newCreds, before.revision)
			if !errors.Is(err, storagesource.ErrSourceUnavailable) {
				t.Fatalf("disabled-source rotation code=%s, want source_unavailable", rotationCode(err))
			}
			f.requireUnchanged(t, before)
		})
		t.Run("disabled after install (detached)", func(t *testing.T) {
			f := newRotationFixture(t, nativeS3PluginID, true)
			f.bindLibrary(t)
			if err := f.r.Disable(t.Context(), f.installationID, f.owner); err != nil {
				t.Fatal(err)
			}
			before := f.state(t)
			_, err := f.rotate(newCreds, before.revision)
			if !errors.Is(err, storagesource.ErrSourceUnavailable) {
				t.Fatalf("detached-source rotation code=%s, want source_unavailable", rotationCode(err))
			}
			f.requireUnchanged(t, before)
		})
	})

	t.Run("a stale expected revision is refused with revision_conflict and unchanged", func(t *testing.T) {
		f := newRotationFixture(t, nativeS3PluginID, true)
		f.bindLibrary(t)
		first := f.state(t)
		if _, err := f.rotate(newCreds, first.revision); err != nil {
			t.Fatalf("first rotation refused: code=%s", rotationCode(err))
		}
		current := f.state(t)
		for _, tc := range []struct {
			name     string
			expected int64
		}{{"behind", first.revision}, {"ahead", current.revision + 1}} {
			_, err := f.rotate(S3Credentials{AccessKey: "synthetic-other-access", SecretKey: "synthetic-other-secret"}, tc.expected)
			var refusal *catalog.NativeOnboardingError
			if !errors.As(err, &refusal) || refusal.Code != "revision_conflict" {
				t.Fatalf("%s: code=%s, want revision_conflict", tc.name, rotationCode(err))
			}
			if refusal.CurrentSourceRevision == nil || *refusal.CurrentSourceRevision != current.revision {
				t.Fatalf("%s: conflict does not report the current revision", tc.name)
			}
			f.requireUnchanged(t, current)
		}
	})

	t.Run("rotation requires retained authority", func(t *testing.T) {
		f := newRotationFixture(t, nativeS3PluginID, true)
		f.bindLibrary(t)
		before := f.state(t)
		if _, err := f.r.RotateCredentials(t.Context(), f.key, f.owner, newCreds, before.revision, nil); rotationCode(err) == "none" {
			t.Fatal("rotation without an authorizer succeeded")
		}
		f.requireUnchanged(t, before)
	})
}
