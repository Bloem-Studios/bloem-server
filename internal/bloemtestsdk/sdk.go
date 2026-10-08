// Package bloemtestsdk locates the Bloem plugin SDK source that storage
// fixture tests build their example provider executable from.
package bloemtestsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// StorageModule is the published Bloem plugin SDK release whose
// examples/hello-storage fixture storage tests execute. Advance it with any
// change to proto/bloem/plugin/v1/storage_provider.proto; StorageDir fails when
// the SDK's copy of the schema no longer matches the host's.
const StorageModule = "github.com/Bloem-Studios/bloem-plugin-sdk@v0.26.0"

// WorktreeEnv names an SDK checkout that overrides StorageModule while a
// developer iterates on an unreleased SDK change.
const WorktreeEnv = "BLOEM_STORAGE_SDK_WORKTREE"

const storageSchema = "proto/bloem/plugin/v1/storage_provider.proto"

// StorageDir returns the SDK source tree: the WorktreeEnv checkout when set,
// otherwise StorageModule downloaded into the module cache.
func StorageDir(t testing.TB, ctx context.Context) string {
	t.Helper()
	dir := os.Getenv(WorktreeEnv)
	if dir == "" {
		download := exec.CommandContext(ctx, "go", "mod", "download", "-json", StorageModule)
		download.Env = append(os.Environ(), "GOWORK=off")
		out, err := download.Output()
		if err != nil {
			t.Fatalf("download %s: %v\n%s", StorageModule, err, out)
		}
		var module struct{ Dir, Error string }
		if err = json.Unmarshal(out, &module); err != nil || module.Error != "" || module.Dir == "" {
			t.Fatalf("download %s: %v %s", StorageModule, err, module.Error)
		}
		dir = module.Dir
	}
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate bloemtestsdk source")
	}
	host, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "..", storageSchema))
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := os.ReadFile(filepath.Join(dir, storageSchema))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(host, sdk) {
		t.Fatalf("SDK at %s carries a different %s than the host; advance bloemtestsdk.StorageModule", dir, storageSchema)
	}
	return dir
}

// BuildStorageFixture builds the SDK's examples/hello-storage executable from
// dir into binary. Extra go build flags (for example -race) precede -o.
func BuildStorageFixture(t testing.TB, ctx context.Context, dir, binary string, flags ...string) {
	t.Helper()
	args := append(append([]string{"build"}, flags...), "-o", binary, "./examples/hello-storage")
	build := exec.CommandContext(ctx, "go", args...)
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build storage fixture: %v\n%s", err, out)
	}
}
