//go:build integration

package nativestorage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

// A source a library reads from cannot be removed, and its view names the
// library only to administrators who may see it. Once released, disabling or
// uninstalling an installation detaches its sources. Changing a
// detached source's configuration or credentials is refused as not_found, as
// the registry refuses it, not reported as native storage being unavailable.
func TestDetachedSourceConfigurationIsNotFoundDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "s3-fixture")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags=-s -w", "-o", path, "./testdata/s3installfixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build storage fixture: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := newFencedSourceFixture(ctx, t, binary)
	key := f.installed.Source.Key
	// The fixture pool has one connection, so the libraries are read directly:
	// a whole view of an attached source would also read its summary.
	libraries := func(actor auth.AdminContextClaims) (bool, []SourceLibrary) {
		t.Helper()
		tx, err := f.management.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		inUse, names, err := sourceLibrariesTx(ctx, tx, actor, key)
		if err != nil {
			t.Fatal(err)
		}
		return inUse, names
	}
	if inUse, names := libraries(f.actor); !inUse || len(names) != 1 || names[0].Name == "" {
		t.Fatalf("platform view of the fixture library's source: in use=%v libraries=%v; want in use, naming it", inUse, names)
	}
	other := f.actor
	other.Scope, other.OrganizationID = auth.AdminScopeOrganization, uuid.New()
	if inUse, names := libraries(other); !inUse || len(names) != 0 {
		t.Fatalf("another organization sees in use=%v libraries=%v; want in use, no names", inUse, names)
	}
	err = f.management.Remove(ctx, f.actor, f.installed.Installation.ID, key, f.installed.Source.ConfigurationRevision, true)
	if refused, ok := errors.AsType[*catalog.NativeOnboardingError](err); !ok || refused.Code != "source_in_use" {
		t.Fatalf("removing a source a library uses = %v, want source_in_use", err)
	}
	if err = f.management.DeleteRemovedSource(ctx, f.actor, key, f.installed.Source.ConfigurationRevision); !isNativeCode(err, "source_attached") {
		t.Fatalf("deleting an attached source = %v, want source_attached", err)
	}
	f.releaseLibraries(ctx, t)
	if err := f.management.Remove(ctx, f.actor, f.installed.Installation.ID, key, f.installed.Source.ConfigurationRevision, false); err != nil {
		t.Fatal("disable", err)
	}
	detached, err := f.management.GetSource(ctx, f.actor, key)
	if err != nil || detached.State != "detached" {
		t.Fatalf("source after disable = %+v, %v; want detached", detached, err)
	}
	for name, change := range map[string]func() error{
		"configuration": func() error {
			_, err := f.management.ReplaceConfiguration(ctx, f.actor, key, detached.ConfigurationRevision, f.cmd.Config)
			return err
		},
		"credentials": func() error {
			_, err := f.management.RotateCredentials(ctx, f.actor, key, detached.ConfigurationRevision, plugins.S3Credentials{AccessKey: "synthetic-access", SecretKey: "synthetic-secret"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := change()
			if refused, ok := errors.AsType[*catalog.NativeOnboardingError](err); !ok || refused.Code != "not_found" {
				t.Fatalf("%s on a detached source = %v %+v, want not_found", name, err, refused)
			}
		})
	}
	// A removed source can then be deleted for good, with the disabled
	// installation it left behind.
	if err = f.management.DeleteRemovedSource(ctx, f.actor, key, detached.ConfigurationRevision); err != nil {
		t.Fatal("delete removed source", err)
	}
	if _, err = f.management.GetSource(ctx, f.actor, key); !isNativeCode(err, "not_found") {
		t.Fatalf("deleted source still readable: %v", err)
	}
	var left int
	if err = f.management.pool.QueryRow(ctx, "SELECT count(*) FROM plugin_installations WHERE id=$1", f.installed.Installation.ID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("disabled installation left behind: %d %v", left, err)
	}
}

func isNativeCode(err error, code string) bool {
	refused, ok := errors.AsType[*catalog.NativeOnboardingError](err)
	return ok && refused.Code == code
}
