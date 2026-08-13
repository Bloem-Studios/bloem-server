# Vondel Plugin SDK Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create, verify, tag, and privately publish `Vondel-Media/vondel-plugin-sdk` as a Vondel-owned Go SDK whose binaries remain wire-compatible with Vondel Server and the pinned official Silo Server baseline.

**Architecture:** Import the official `silo-plugin-sdk` v0.13.2 source snapshot into a private repository with a clean Vondel root commit, then change only build-time Go import identity and project documentation. Preserve the protobuf package, serialized fields, capability values, go-plugin handshake, and manifest compatibility keys, and protect them with explicit contract tests before any downstream plugin consumes the new module.

**Tech Stack:** Go 1.26, Protocol Buffers, gRPC, HashiCorp go-plugin, Buf/protoc, GitHub Actions, GitHub private repositories and releases.

## Global Constraints

- The repository and every tag, release, package, build artifact, CI log, and URL remain private until the owner explicitly approves publication.
- Import exactly upstream tag `v0.13.2`, commit `1ad0fe54408e99d35e6aee86c489a0edd528f6b2`, from `https://github.com/Silo-Server/silo-plugin-sdk`.
- Use a clean zero-parent Vondel root commit while preserving all required Apache-2.0 copyright and license notices in the imported snapshot.
- Use Go module path `github.com/Vondel-Media/vondel-plugin-sdk`.
- Preserve protobuf package `silo.plugin.v1`, manifest key `silo_api_version`, value `v1`, capability names, field numbers, service names, `SILO_PLUGIN`, `silo-rpc-plugin-v1`, and plugin-set name `silo`.
- Never embed a private GitHub token, private clone URL containing credentials, or personal filesystem path in source, workflows, binaries, fixtures, or documentation.
- Release builds use `GOWORK=off` and contain no committed `replace` directive.
- Do not modify Vondel Server's SDK dependency in this plan; that belongs to the server-integration plan after the SDK tag exists.

---

### Task 1: Import the Upstream SDK into a Private Vondel Repository

**Files:**
- Create repository working tree: `/Users/jimcole/projects/vondel-plugin-sdk`
- Preserve: `LICENSE`
- Preserve source snapshot: all tracked files from upstream tag `v0.13.2`

**Interfaces:**
- Consumes: official upstream tag `v0.13.2` at commit `1ad0fe54408e99d35e6aee86c489a0edd528f6b2`.
- Produces: private GitHub repository `Vondel-Media/vondel-plugin-sdk`, local `main`, fetch-only `upstream`, and writable `origin`.

- [ ] **Step 1: Verify the source revision and destination visibility before copying**

Run:

```bash
gh api repos/Silo-Server/silo-plugin-sdk/git/ref/tags/v0.13.2 --jq '.object.sha'
gh repo view Vondel-Media/vondel-plugin-sdk --json visibility 2>/dev/null || true
```

Expected: upstream resolves to `1ad0fe54408e99d35e6aee86c489a0edd528f6b2`; the Vondel repository does not yet exist.

- [ ] **Step 2: Create an isolated source snapshot**

Run:

```bash
git clone --branch v0.13.2 --single-branch https://github.com/Silo-Server/silo-plugin-sdk.git /Users/jimcole/projects/vondel-plugin-sdk
git -C /Users/jimcole/projects/vondel-plugin-sdk rev-parse HEAD
git -C /Users/jimcole/projects/vondel-plugin-sdk status --porcelain
```

Expected: the printed revision is the pinned SHA and status is empty.

- [ ] **Step 3: Verify the imported baseline before changing identity**

Run from `/Users/jimcole/projects/vondel-plugin-sdk`:

```bash
GOWORK=off go mod download
GOWORK=off go test ./...
```

Expected: all upstream SDK and example-package tests PASS before Vondel changes.

- [ ] **Step 4: Replace the imported history with a clean root without changing files**

Run from `/Users/jimcole/projects/vondel-plugin-sdk`:

```bash
source_sha="$(git rev-parse HEAD)"
git switch --orphan vondel-root
git read-tree "$source_sha"
git checkout-index -a
git add -A
git commit -m "Initial Vondel Plugin SDK import"
git branch -M main
git remote rename origin upstream
git remote set-url --push upstream DISABLED
test "$(git rev-list --parents -n 1 HEAD | wc -w | tr -d ' ')" = 1
```

Expected: `main` contains one zero-parent snapshot commit and upstream cannot receive pushes.

- [ ] **Step 5: Create the private remote and push the snapshot**

Run:

```bash
gh repo create Vondel-Media/vondel-plugin-sdk \
  --private \
  --description "Private Vondel plugin authoring SDK" \
  --source=. \
  --remote=origin
git push -u origin main
gh api repos/Vondel-Media/vondel-plugin-sdk --jq '{visibility,private,default_branch}'
```

Expected: visibility is `private`, `private` is `true`, and default branch is `main`.

- [ ] **Step 6: Commit is already created; record the import revision in the next legal-identity task**

No additional commit is created in this step. Verify `git status --porcelain` is empty.

---

### Task 2: Establish Vondel Legal and Go-Module Identity

**Files:**
- Create: `NOTICE`
- Modify: `README.md`
- Modify: `docs/compatibility.md`
- Modify: `docs/runtime-host.md`
- Modify: `go.mod`
- Modify: `proto/silo/plugin/v1/*.proto`
- Modify: `pkg/**/*.go`
- Modify: `examples/**/*.go`
- Test: `internal/projectidentity/identity_test.go`

**Interfaces:**
- Consumes: clean imported SDK snapshot from Task 1.
- Produces: Go module `github.com/Vondel-Media/vondel-plugin-sdk`; generated Go code imports resolve through that module while wire identifiers remain unchanged.

- [ ] **Step 1: Write a failing project-identity test**

Create `internal/projectidentity/identity_test.go`:

```go
package projectidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVondelModuleAndAttribution(t *testing.T) {
	root := filepath.Join("..", "..")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(goMod), "module github.com/Vondel-Media/vondel-plugin-sdk\n") {
		t.Fatalf("unexpected module declaration: %s", goMod)
	}
	notice, err := os.ReadFile(filepath.Join(root, "NOTICE"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Silo Plugin SDK",
		"v0.13.2",
		"1ad0fe54408e99d35e6aee86c489a0edd528f6b2",
		"Apache-2.0",
		"not affiliated with or endorsed by Silo Media L.L.C.",
	} {
		if !strings.Contains(string(notice), required) {
			t.Errorf("NOTICE missing %q", required)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
go test ./internal/projectidentity -run TestVondelModuleAndAttribution -count=1
```

Expected: FAIL because the module still uses `Silo-Server` and `NOTICE` does not exist.

- [ ] **Step 3: Add attribution and replace build-time module imports**

Create `NOTICE` with this content:

```text
Vondel Plugin SDK
=================

This project is an independent fork of Silo Plugin SDK v0.13.2, upstream
revision 1ad0fe54408e99d35e6aee86c489a0edd528f6b2, from
https://github.com/Silo-Server/silo-plugin-sdk.

The source remains licensed under Apache-2.0. Existing copyright and license
notices are retained. Vondel changes the project/module identity, documentation,
examples, compatibility verification, and private release automation while
preserving the v1 plugin wire contract.

Vondel is not affiliated with or endorsed by Silo Media L.L.C. References to
Silo identify the upstream project or compatibility interfaces.
```

Mechanically replace `github.com/Silo-Server/silo-plugin-sdk` with
`github.com/Vondel-Media/vondel-plugin-sdk` in `go.mod`, Go imports, and every
`option go_package` declaration. Do not replace `package silo.plugin.v1`, proto
paths, manifest JSON keys, handshake constants, or capability values.

Update the README title to `vondel-plugin-sdk`, describe it as the private
Vondel authoring SDK, link the upstream project in its attribution section, and
state that its plugins target both Vondel and compatible official Silo servers.

- [ ] **Step 4: Format, tidy, and rerun the identity test**

Run:

```bash
gofmt -w $(rg --files internal/projectidentity pkg examples -g '*.go')
GOWORK=off go mod tidy
go test ./internal/projectidentity -run TestVondelModuleAndAttribution -count=1
```

Expected: PASS.

- [ ] **Step 5: Verify no stale build-time module imports remain**

Run:

```bash
if rg -n 'github.com/Silo-Server/silo-plugin-sdk' \
  --glob '*.go' --glob '*.proto' --glob 'go.mod' .; then
  exit 1
fi
rg -n 'package silo\.plugin\.v1|silo_api_version|silo-rpc-plugin-v1' proto pkg examples
```

Expected: the first search returns no matches; the compatibility search finds the preserved wire identifiers.

- [ ] **Step 6: Commit legal and module identity**

```bash
git add NOTICE README.md docs go.mod go.sum proto pkg examples internal/projectidentity
git commit -m "chore: establish Vondel SDK identity"
```

---

### Task 3: Lock the V1 Wire Contract with Compatibility Tests

**Files:**
- Create: `compat/v1_contract_test.go`
- Create: `compat/source_guard_test.go`
- Modify: `pkg/pluginsdk/runtime/runtime_test.go`

**Interfaces:**
- Consumes: generated `pluginv1` descriptors and runtime constants.
- Produces: executable guarantees for protobuf namespace, manifest field number, services, capability values, and go-plugin handshake.

- [ ] **Step 1: Write failing descriptor and handshake assertions**

Create `compat/v1_contract_test.go`:

```go
package compat

import (
	"testing"

	pluginv1 "github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginsdk/capability"
	"github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginsdk/runtime"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestV1WireContract(t *testing.T) {
	file := pluginv1.File_silo_plugin_v1_common_proto
	if got := string(file.Package()); got != "silo.plugin.v1" {
		t.Fatalf("protobuf package = %q", got)
	}
	manifest := file.Messages().ByName("PluginManifest")
	if manifest == nil {
		t.Fatal("PluginManifest descriptor missing")
	}
	field := manifest.Fields().ByName("silo_api_version")
	if field == nil || field.Number() != protoreflect.FieldNumber(4) {
		t.Fatalf("silo_api_version field = %#v", field)
	}

	services := []struct {
		file protoreflect.FileDescriptor
		name protoreflect.Name
	}{
		{pluginv1.File_silo_plugin_v1_common_proto, "Runtime"},
		{pluginv1.File_silo_plugin_v1_metadata_provider_proto, "MetadataProvider"},
		{pluginv1.File_silo_plugin_v1_scan_source_proto, "ScanSource"},
		{pluginv1.File_silo_plugin_v1_runtime_host_proto, "RuntimeHost"},
	}
	for _, service := range services {
		desc := service.file.Services().ByName(service.name)
		if desc == nil || string(desc.FullName()) != "silo.plugin.v1."+string(service.name) {
			t.Errorf("service %s changed: %#v", service.name, desc)
		}
	}

	if runtime.MagicCookieKey != "SILO_PLUGIN" ||
		runtime.MagicCookieValue != "silo-rpc-plugin-v1" ||
		runtime.PluginSetName != "silo" || runtime.ProtocolVersion != 1 {
		t.Fatal("go-plugin handshake contract changed")
	}

	for _, required := range []string{
		"metadata_provider.v1",
		"image_resolver.v1",
		"scan_source.v1",
	} {
		found := false
		for _, actual := range capability.KnownTypes {
			found = found || actual == required
		}
		if !found {
			t.Errorf("capability %q missing", required)
		}
	}
}
```

- [ ] **Step 2: Temporarily change one expected constant and prove the guard fails**

Change the test's expected magic-cookie value to `vondel-rpc-plugin-v1`, then run:

```bash
go test ./compat -run TestV1WireContract -count=1
```

Expected: FAIL with `go-plugin handshake contract changed`. Restore the expected value to `silo-rpc-plugin-v1`.

- [ ] **Step 3: Add a source guard against accidental protocol rebranding**

Create `compat/source_guard_test.go` with a test that walks `proto/` and fails if
any `.proto` file contains `package vondel.plugin`, `vondel_api_version`, or an
`option go_package` outside `github.com/Vondel-Media/vondel-plugin-sdk`. The same
test must verify at least one `.proto` file was inspected so an empty directory
cannot pass.

Use this implementation:

```go
package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtoSourcesPreserveWireIdentity(t *testing.T) {
	root := filepath.Join("..", "proto")
	inspected := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}
		inspected++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, forbidden := range []string{"package vondel.plugin", "vondel_api_version"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains forbidden wire rename %q", path, forbidden)
			}
		}
		const goPackage = "option go_package = \"github.com/Vondel-Media/vondel-plugin-sdk/"
		if !strings.Contains(text, goPackage) {
			t.Errorf("%s does not use the Vondel build-time Go package", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inspected == 0 {
		t.Fatal("no protobuf sources inspected")
	}
}
```

- [ ] **Step 4: Run compatibility and SDK tests**

Run:

```bash
GOWORK=off go test ./compat ./pkg/...
```

Expected: PASS.

- [ ] **Step 5: Commit the compatibility boundary**

```bash
git add compat pkg/pluginsdk/runtime/runtime_test.go
git commit -m "test: lock the Silo v1 wire contract"
```

---

### Task 4: Rebrand and Verify the Author Examples

**Files:**
- Modify: `examples/hello-scheduled-task/README.md`
- Modify: `examples/hello-scheduled-task/main.go`
- Modify: `examples/hello-scheduled-task/manifest.json`
- Modify: `examples/hello-runtime-host/main.go`
- Modify: `examples/hello-runtime-host/manifest.json`
- Test: `examples/examples_test.go`

**Interfaces:**
- Consumes: Vondel module imports and preserved `silo_api_version: v1` contract.
- Produces: self-describing sample plugins that build solely against the Vondel module.

- [ ] **Step 1: Write a failing manifest identity test**

Create `examples/examples_test.go` that loads both example manifests through
`manifest.Load`, then asserts:

```go
package examples

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestExampleManifestIdentity(t *testing.T) {
	tests := []struct {
		path string
		id   string
	}{
		{"hello-scheduled-task/manifest.json", "vondel.example.hello-task"},
		{"hello-runtime-host/manifest.json", "vondel.example.runtime-host"},
	}
	for _, tc := range tests {
		t.Run(filepath.Base(filepath.Dir(tc.path)), func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			m, err := manifest.Load(data)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.GetPluginId(); got != tc.id {
				t.Fatalf("plugin_id = %q, want %q", got, tc.id)
			}
			if got := m.GetSiloApiVersion(); got != "v1" {
				t.Fatalf("silo_api_version = %q", got)
			}
		})
	}
}
```

The expected plugin IDs are `vondel.example.hello-task` and
`vondel.example.runtime-host`.

- [ ] **Step 2: Run the example test and verify it fails**

Run:

```bash
go test ./examples -count=1
```

Expected: FAIL because the imported example IDs do not match the Vondel IDs.

- [ ] **Step 3: Rebrand only user-facing example identity**

Change example plugin IDs, names, README copy, and Go imports to Vondel. Preserve
`silo_api_version`, runtime handshake constants, protobuf paths, and capability
type strings.

- [ ] **Step 4: Build and test both examples**

Run:

```bash
GOWORK=off go test ./examples -count=1
GOWORK=off go build ./examples/hello-scheduled-task
GOWORK=off go build ./examples/hello-runtime-host
```

Expected: all commands PASS without a workspace or replace directive.

- [ ] **Step 5: Commit example identity**

```bash
git add examples
git commit -m "docs: add Vondel plugin examples"
```

---

### Task 5: Add Private-Only CI and Release Automation

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Create: `scripts/verify-private-release.sh`
- Create: `docs/private-release.md`
- Test: `scripts/verify-private-release.sh`

**Interfaces:**
- Consumes: Vondel module, compatibility tests, and author examples.
- Produces: private CI and tag-driven private releases; no workflow changes repository visibility or publishes a public package.

- [ ] **Step 1: Write a failing private-release guard**

Create `scripts/verify-private-release.sh`:

```sh
#!/bin/sh
set -eu

fail() {
  printf 'private release guard failed: %s\n' "$1" >&2
  exit 1
}

test "$(sed -n '1s/^module //p' go.mod)" = \
  "github.com/Vondel-Media/vondel-plugin-sdk" || fail "unexpected module"

if grep -Eq '^[[:space:]]*replace[[:space:]]|^replace[[:space:]]*\(' go.mod; then
  fail "go.mod contains a replace directive"
fi

if rg -n 'gh repo edit.*visibility|npm publish|docker push|pkg\.go\.dev' .github; then
  fail "workflow contains a public publication path"
fi

if rg -n '/Users/|/home/[^/]+/|https://[^/@]+:[^/@]+@github\.com' \
  --glob '!scripts/verify-private-release.sh' .; then
  fail "repository contains a local path or credential-bearing URL"
fi

printf '%s\n' 'private release guard passed'
```

Make it executable. Temporarily append `replace example.invalid => ../sdk` to
`go.mod`, run the guard, and verify it fails. Remove the temporary line.

- [ ] **Step 2: Replace upstream automation identity**

Update CI to use Go 1.26 and run, in order:

```yaml
- run: ./scripts/verify-private-release.sh
- run: GOWORK=off go test ./...
- run: GOWORK=off go build ./examples/hello-scheduled-task
- run: GOWORK=off go build ./examples/hello-runtime-host
```

Remove Silo organization environment patterns. Release automation triggers only
on an explicitly pushed `v*` tag, tests that exact tag, and creates a GitHub
release in the already-private repository. Remove workflow dispatch logic that
auto-increments and pushes an unreviewed tag.

- [ ] **Step 3: Document private tag creation and rollback**

In `docs/private-release.md`, specify this exact operator sequence:

```bash
git status --porcelain
GOWORK=off go test ./...
./scripts/verify-private-release.sh
git tag -a v0.13.2 -m "Vondel Plugin SDK v0.13.2"
git push origin v0.13.2
gh release view v0.13.2 --repo Vondel-Media/vondel-plugin-sdk
gh api repos/Vondel-Media/vondel-plugin-sdk --jq '.visibility'
```

Document rollback as deleting the private release and tag only before any
downstream repository pins it; after a downstream pin, issue a new patch tag.

- [ ] **Step 4: Run the full local release gate**

Run:

```bash
./scripts/verify-private-release.sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./examples/hello-scheduled-task
GOWORK=off go build ./examples/hello-runtime-host
git diff --check
```

Expected: all commands PASS.

- [ ] **Step 5: Commit private automation**

```bash
git add .github scripts docs/private-release.md
git commit -m "ci: add private SDK release gate"
git push origin main
```

---

### Task 6: Build an Interoperability Probe Artifact

**Files:**
- Create: `cmd/compat-probe/main.go`
- Create: `cmd/compat-probe/main_test.go`
- Create: `cmd/compat-probe/manifest.json`
- Create: `scripts/build-compat-probe.sh`

**Interfaces:**
- Consumes: Vondel SDK runtime and `silo.plugin.v1` generated types.
- Produces: self-describing binary `dist/compat-probe` with plugin ID `vondel.compat.probe`, `silo_api_version: v1`, and `metadata_provider.v1`; later plans install this exact binary on Vondel and pinned official Silo servers.

- [ ] **Step 1: Write a failing self-description test**

Create `cmd/compat-probe/main_test.go` that builds the package binary into
`t.TempDir()`, executes `<binary> manifest`, decodes stdout with
`manifest.Load`, and asserts. Use this implementation:

```go
package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestManifestSubcommand(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "compat-probe")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, output)
	}
	output, err := exec.Command(binary, "manifest").CombinedOutput()
	if err != nil {
		t.Fatalf("probe manifest: %v\n%s", err, output)
	}
	m, err := manifest.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.GetPluginId(); got != "vondel.compat.probe" {
		t.Fatalf("plugin_id = %q", got)
	}
	if got := m.GetSiloApiVersion(); got != "v1" {
		t.Fatalf("silo_api_version = %q", got)
	}
	if len(m.GetCapabilities()) != 1 ||
		m.GetCapabilities()[0].GetType() != "metadata_provider.v1" {
		t.Fatalf("capabilities = %#v", m.GetCapabilities())
	}
}
```

- [ ] **Step 2: Run the probe test and verify it fails**

Run:

```bash
go test ./cmd/compat-probe -count=1
```

Expected: FAIL because the command does not exist.

- [ ] **Step 3: Implement the smallest self-describing metadata provider**

Implement `main.go` with `runtimedefault.Runtime`, the SDK's manifest command,
and a metadata provider that returns an empty successful match response for a
probe request. Embed `manifest.json`, calculate the executable checksum through
the existing manifest helper, and serve through `runtime.Serve` when the command
is not `manifest`.

Use this implementation (the embedded unimplemented server supplies explicit
`Unimplemented` responses for capability methods not exercised by the probe):

```go
package main

import (
	"context"
	_ "embed"

	pluginv1 "github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Vondel-Media/vondel-plugin-sdk/pkg/pluginsdk/runtime"
)

const version = "0.13.2"

//go:embed manifest.json
var manifestJSON []byte

type metadataProvider struct {
	pluginv1.UnimplementedMetadataProviderServer
}

func (*metadataProvider) Search(context.Context, *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	return &pluginv1.SearchMetadataResponse{}, nil
}

func main() {
	sdkruntime.ServeManifest(manifestJSON, version, sdkruntime.CapabilityServers{
		MetadataProvider: &metadataProvider{},
	})
}
```

The manifest contains:

```json
{
  "plugin_id": "vondel.compat.probe",
  "version": "0.13.2",
  "checksum": "__CHECKSUM__",
  "silo_api_version": "v1",
  "capabilities": [
    {
      "type": "metadata_provider.v1",
      "id": "compat-probe",
      "display_name": "Vondel Compatibility Probe",
      "description": "Verifies the shared v1 plugin runtime contract."
    }
  ]
}
```

- [ ] **Step 4: Build a reproducible local artifact and checksum**

Create `scripts/build-compat-probe.sh` to run:

```sh
#!/bin/sh
set -eu
mkdir -p dist
GOWORK=off CGO_ENABLED=0 go build -trimpath -o dist/compat-probe ./cmd/compat-probe
shasum -a 256 dist/compat-probe > dist/compat-probe.sha256
dist/compat-probe manifest > dist/compat-probe.manifest.json
```

Do not commit `dist/`; add it to `.gitignore`.

- [ ] **Step 5: Run probe and full SDK verification**

Run:

```bash
go test ./cmd/compat-probe -count=1
./scripts/build-compat-probe.sh
go test ./...
git diff --check
```

Expected: PASS, and the three local `dist/compat-probe*` files exist but are untracked/ignored.

- [ ] **Step 6: Commit the probe source**

```bash
git add .gitignore cmd/compat-probe scripts/build-compat-probe.sh
git commit -m "test: add v1 interoperability probe"
git push origin main
```

---

### Task 7: Tag and Verify the Private SDK Release

**Files:**
- No source changes expected.
- Verify remote tag: `v0.13.2`
- Verify private GitHub release: `Vondel-Media/vondel-plugin-sdk/releases/tag/v0.13.2`

**Interfaces:**
- Consumes: green `main`, private release workflow, and interoperability probe.
- Produces: immutable private SDK dependency `github.com/Vondel-Media/vondel-plugin-sdk@v0.13.2` for the core-plugin plan.

- [ ] **Step 1: Re-run all release evidence from a clean worktree**

Run:

```bash
test -z "$(git status --porcelain)"
./scripts/verify-private-release.sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
./scripts/build-compat-probe.sh
```

Expected: all commands PASS.

- [ ] **Step 2: Confirm repository privacy immediately before tagging**

Run:

```bash
test "$(gh api repos/Vondel-Media/vondel-plugin-sdk --jq '.visibility')" = private
```

Expected: PASS.

- [ ] **Step 3: Create and push the reviewed SDK tag**

Run:

```bash
git tag -a v0.13.2 -m "Vondel Plugin SDK v0.13.2"
git push origin v0.13.2
```

Expected: tag push succeeds and starts the private Release workflow.

- [ ] **Step 4: Wait for CI and release workflows and inspect failures**

Run:

```bash
gh run list --repo Vondel-Media/vondel-plugin-sdk --limit 10
run_id="$(gh run list --repo Vondel-Media/vondel-plugin-sdk \
  --workflow Release --branch v0.13.2 --limit 1 \
  --json databaseId --jq '.[0].databaseId')"
gh run watch "$run_id" --repo Vondel-Media/vondel-plugin-sdk --exit-status
gh release view v0.13.2 --repo Vondel-Media/vondel-plugin-sdk --json isDraft,isPrerelease,tagName,url
```

Expected: CI and Release conclude successfully; the release exists in the private repository.

- [ ] **Step 5: Verify external module resolution with an ephemeral credential**

Use an organization read token supplied only through `VONDEL_PRIVATE_MODULE_TOKEN`:

```bash
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
cd "$tmpdir"
go mod init vondel-sdk-consumer
GIT_CONFIG_COUNT=1 \
GIT_CONFIG_KEY_0="url.https://x-access-token:${VONDEL_PRIVATE_MODULE_TOKEN}@github.com/.insteadOf" \
GIT_CONFIG_VALUE_0="https://github.com/" \
GOPRIVATE=github.com/Vondel-Media/* GONOSUMDB=github.com/Vondel-Media/* \
  go get github.com/Vondel-Media/vondel-plugin-sdk@v0.13.2
GOWORK=off go list -m all | grep 'github.com/Vondel-Media/vondel-plugin-sdk v0.13.2'
```

Expected: the private tagged module resolves without exposing the token in repository files or command output.

- [ ] **Step 6: Record the handoff in Vondel Server documentation**

Append the SDK repository URL, tag, commit SHA, CI run URLs, and preserved wire
contracts to the execution notes section of this plan. Commit that documentation
in `vondel-server` separately:

```bash
git add docs/superpowers/plans/2026-08-12-vondel-plugin-sdk-foundation.md
git commit -m "docs: record private Vondel SDK release"
git push origin main
```

The next implementation plan is `vondel-plugins` plus MetaDB, TMDB, and TVDB;
it must pin `github.com/Vondel-Media/vondel-plugin-sdk v0.13.2` and must not use a
local replace directive.

## Execution Notes

The SDK remains private at
<https://github.com/Vondel-Media/vondel-plugin-sdk>.

The initially planned `v0.13.2` tag is a failed private release candidate at
commit `71308e583a3cc1c0acaae0af48bbd11bf6b9b633`. Its Release workflow failed
because the GitHub-hosted runner did not provide the `rg` command required by
the fail-closed private-release guard:
<https://github.com/Vondel-Media/vondel-plugin-sdk/actions/runs/31592408485>.
No GitHub Release was created for `v0.13.2`, the tag was not rewritten, and it
must not be used as a downstream dependency pin.

The workflow remediation was reviewed at commit
`50b99ea65028431e27108eb0da8db537a016d3b4`
(`ci: provision ripgrep for release guards`). Its push-triggered CI run passed:
<https://github.com/Vondel-Media/vondel-plugin-sdk/actions/runs/31592910249>.

The verified private SDK handoff is the annotated tag `v0.13.3`, which resolves
to the reviewed remediation commit
`50b99ea65028431e27108eb0da8db537a016d3b4`. The exact tag-triggered Release
workflow passed its private-release guard, Go tests, example builds, immutable
remote-tag check, and GitHub Release creation:
<https://github.com/Vondel-Media/vondel-plugin-sdk/actions/runs/31593214058>.
The resulting private, non-draft, non-prerelease GitHub Release is
<https://github.com/Vondel-Media/vondel-plugin-sdk/releases/tag/v0.13.3>.
A fresh external module and module cache resolved
`github.com/Vondel-Media/vondel-plugin-sdk v0.13.3` using configured private
GitHub authentication, with no credential persisted or printed.

The tagged SDK preserves the `silo.plugin.v1` protobuf package,
`silo_api_version` manifest key and `v1` value, field number 4, plugin-set name
`silo`, protocol version 1, `SILO_PLUGIN` / `silo-rpc-plugin-v1` handshake,
service names including `Runtime`, `MetadataProvider`, `ScanSource`, and
`RuntimeHost`, and capability values including `metadata_provider.v1`,
`image_resolver.v1`, and `scan_source.v1`.

The next implementation plan for `vondel-plugins` plus MetaDB, TMDB, and TVDB
must pin `github.com/Vondel-Media/vondel-plugin-sdk v0.13.3` and must not use a
local `replace` directive.
