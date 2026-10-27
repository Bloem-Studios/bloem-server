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

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
)

// Disabling or uninstalling an installation detaches its sources. Changing a
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
}
