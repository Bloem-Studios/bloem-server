# Storage Source Management UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a platform admin add, inspect, test, edit, rotate keys for, disable and remove S3 and Bookwarehouse storage sources from the Bloem admin UI, with any number of S3 sources (one per bucket).

**Architecture:** The host catalog learns a second plugin (S3) whose downloads must match a pre-approved digest; a new JSON "catalog discover" call installs it and discovers its root; source documents carry a non-secret `connection_summary`; a new credentials call rotates keys on in-use S3 sources; the web Add-source dialog renders the plugin manifest's own form. The library picker explains an empty root using the summary.

**Tech Stack:** Go (chi, pgx, protojson), React 19 + TypeScript + TanStack Query + Vitest + Playwright, the `Bloem-Studios/bloem-storage-s3` plugin repo and the `Bloem-Studios/bloem-plugins` catalog repo.

**Spec:** `docs/architecture/bloem-storage-source-management-ui-design.md` (read it first; this plan implements it).

## Global Constraints

- S3 add is **platform scope only**; organization scope must return `404 not_found` for the new call and the UI must not offer S3 add there.
- Stored config is encrypted and **never returned**; secrets (`access_key`, `secret_key`, `session_token`) never appear in any response, summary, log line or test snapshot.
- S3 config lives in one object named `connection`; `state_path` is never sent or accepted.
- S3 `prefix` is empty or ends with `/` (plugin rule); `endpoint` is HTTPS with no path/credentials.
- `provider_source_id` must equal `config.connection.source_id`.
- Credential rotation overlays **only** `access_key`, `secret_key`, `session_token`.
- Bloem footprint rule (`BLOEM_AGENTS.md`): put new server code in new `bloem_*` files; edit shared Silo files only for integration points and record each one in the seam ledger (Task 14).
- Nothing is deployed or pushed by this plan. Publishing the catalog entry and deploying are separate approvals.
- Commit trailer on every commit: `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Prefix typed without trailing slash or with a leading slash** (`movies`, `/movies/`) → the form shows a field error before submit, and the server's rejection is mapped to the same field. (Task 8)
2. **Endpoint with `http://`, a path, or embedded credentials** → rejected with a field-level message, nothing created. (Task 8)
3. **Plugin catalog unreachable or slow** → the Add dialog shows "plugin catalog unavailable" with Retry and never hangs. (Task 8)
4. **Secrets leaking** into `connection_summary`, form state after submit, or query cache → property test over fixture configs; form clears secret fields after success. (Tasks 3, 8)
5. **Rotation request that also tries to change the bucket/prefix** or targets a Bookwarehouse/disabled source → refused, source unchanged. (Task 5)
6. **Two S3 sources with identical bucket+prefix** → allowed but the dialog warns. (Task 8)

---

## File Structure

Server (new files in bold):
- `internal/plugins/bloem_storage_catalog.go` — modify: per-plugin catalog specs, pinned-digest rule.
- `internal/plugins/bloem_storage_catalog_test.go` — modify: update helpers, add S3 tests.
- `internal/plugins/bloem_storage_management.go` — modify: artifact view gains `catalog`.
- **`internal/plugins/bloem_storage_connection.go`** — `ConnectionConfig` reader and `RotateCredentials`.
- **`internal/plugins/bloem_storage_connection_test.go`**
- `internal/plugins/bloem_storage_registry.go` — modify: `pinned` set; `replaceConfiguration` gains a rotation flag.
- **`internal/nativestorage/connection_summary.go`** / **`connection_summary_test.go`** — pure summary allowlist.
- `internal/nativestorage/source_management_store.go` — modify: attach summary in `sourceViewTx`.
- `internal/nativestorage/source_management.go` — modify: `SourceView.ConnectionSummary`; `RotateCredentials`.
- **`internal/api/handlers/bloem_native_storage_catalog_discovery.go`** / **`..._test.go`** — new handler.
- **`internal/api/handlers/bloem_native_storage_credentials.go`** / **`..._test.go`** — rotation handler.
- `internal/api/handlers/bloem_native_storage_management.go` — modify: artifacts handler adds `config_schema`; capabilities.
- `internal/api/router_bloem_native_storage_management.go` — modify: two routes.
- `internal/apiv2/bloem_native_storage_document.go`, `docs/bloem-api-reference.md`, `docs/architecture/bloem-native-storage.md`, `contracts/api/bloem/v1/openapi.json` — contract and docs.

Web:
- `web/src/hooks/queries/admin/storageSources.ts` (+ test) — types and mutations.
- **`web/src/components/admin/plugins/AddStorageSourceDialog.tsx`** (+ test) — manifest-driven add.
- **`web/src/components/admin/plugins/storageSchema.ts`** (+ test) — prune hidden fields, prefix/endpoint checks.
- **`web/src/components/admin/plugins/StorageSourceRow.tsx`** (+ test) — summary, actions, edit, rotate.
- `web/src/components/admin/plugins/StorageSourcesPanel.tsx` — modify: use the new pieces.
- `web/src/bloem/library-locations/StorageFolderBrowser.tsx` (+ test) — empty-state text.
- `web/e2e/storage-sources.spec.ts` — new journey.

Other repos: `bloem-storage-s3` regression tests (Task 11), `bloem-plugins` `plugins.json` (Task 13).

All Go commands run from the repository root of the bloem-server worktree; web commands from `web/`.

---

### Task 1: Catalog specs and pinned S3 admission

**Files:**
- Modify: `internal/plugins/bloem_storage_catalog.go`
- Modify: `internal/plugins/bloem_storage_registry.go` (struct field + constructor)
- Modify: `internal/plugins/bloem_storage_management.go` (`NativeStorageArtifactView`, `ApprovedArtifacts`)
- Test: `internal/plugins/bloem_storage_catalog_test.go`

**Interfaces:**
- Produces: `nativeCatalogSpec{PluginID, KeyPrefix, ReleaseTag string; RequirePinned bool}`, `nativeCatalogSpecFor(pluginID string) (nativeCatalogSpec, bool)`, `storageCatalogURL(spec nativeCatalogSpec, raw, version, file string) bool`, `catalogArtifactKey(spec nativeCatalogSpec, version, checksum string) string`, registry field `pinned map[string]bool` (checksums of statically approved artifacts), `NativeStorageArtifactView.Catalog bool` (`json:"catalog"`).

- [ ] **Step 1: Write the failing tests.** In `bloem_storage_catalog_test.go`, replace the two existing tests' calls to the changed helpers and add a fixture helper plus S3 tests. Replace the whole file with:

```go
package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type storageCatalogTransport func(*http.Request) (*http.Response, error)

func (f storageCatalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var (
	bookwarehouseSpec = nativeCatalogSpec{PluginID: "bloem.storage.bookwarehouse", KeyPrefix: "bookwarehouse", ReleaseTag: "bookwarehouse-v"}
	s3Spec            = nativeCatalogSpec{PluginID: "bloem.storage.s3", KeyPrefix: "s3", ReleaseTag: "s3-v", RequirePinned: true}
)

// catalogFixture serves a one-release inventory and manifest for spec/version.
func catalogFixture(t *testing.T, spec nativeCatalogSpec, version, checksum string) *http.Client {
	t.Helper()
	manifest := &publicv1.PluginManifest{PluginId: spec.PluginID, Version: version, SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	raw, err := protojson.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + spec.ReleaseTag + version + "/plugin-" + runtime.GOOS + "-" + runtime.GOARCH
	inventory, _ := json.Marshal(map[string]any{"plugins": []any{map[string]any{"plugin_id": spec.PluginID, "source_version": version,
		"binaries": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]any{"url": url, "checksum": checksum}}}}})
	return &http.Client{Transport: storageCatalogTransport(func(r *http.Request) (*http.Response, error) {
		body := inventory
		if strings.HasSuffix(r.URL.Path, "manifest.json") {
			body = raw
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
}

func TestNativeCatalogAdmitsWithoutManualApprovalAndPersists(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	root := t.TempDir()
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = catalogFixture(t, bookwarehouseSpec, "0.2.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CatalogBinary(context.Background(), catalogArtifactKey(bookwarehouseSpec, "0.2.2", checksum)); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	artifacts := registry.ApprovedArtifacts()
	if len(artifacts) != 1 || artifacts[0].PluginID != "bloem.storage.bookwarehouse" || !artifacts[0].Catalog {
		t.Fatalf("catalog missing: %+v", artifacts)
	}
	reloaded := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	if err := reloaded.loadCatalog(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ApprovedArtifacts()) != 1 {
		t.Fatal("installed catalog trust requires manual approval or refresh after restart")
	}
}

func TestNativeCatalogS3RequiresPinnedDigest(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = catalogFixture(t, s3Spec, "0.3.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := registry.ApprovedArtifacts(); len(got) != 0 {
		t.Fatalf("unpinned S3 release was admitted: %+v", got)
	}
}

func TestNativeCatalogAdmitsPinnedS3AndFlagsCatalog(t *testing.T) {
	checksum := strings.Repeat("c", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: map[string]bool{checksum: true}}
	registry.catalogClient = catalogFixture(t, s3Spec, "0.3.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := registry.ApprovedArtifacts()
	if len(got) != 1 || got[0].PluginID != "bloem.storage.s3" || !got[0].Catalog || !strings.HasPrefix(got[0].ArtifactKey, "s3-0.3.2-") {
		t.Fatalf("pinned S3 release not admitted as catalog artifact: %+v", got)
	}
}

func TestNativeCatalogRejectsExternalBinaryURLs(t *testing.T) {
	if storageCatalogURL(bookwarehouseSpec, "https://attacker.example/plugin", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("external URL accepted")
	}
	if storageCatalogURL(bookwarehouseSpec, "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.2.2/plugin-linux-amd64?token=secret", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("query accepted")
	}
	if storageCatalogURL(s3Spec, "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.3.2/plugin-linux-amd64", "0.3.2", "plugin-linux-amd64") {
		t.Fatal("another plugin's release tag accepted for S3")
	}
}
```

- [ ] **Step 2: Run to verify failure.** Run: `go test ./internal/plugins -run 'TestNativeCatalog' -count=1`. Expected: build FAIL (`undefined: nativeCatalogSpec`, `registry.pinned`, `Catalog`).

- [ ] **Step 3: Implement.** In `bloem_storage_catalog.go`: remove `const nativeCatalogPlugin`; add

```go
type nativeCatalogSpec struct {
	PluginID      string
	KeyPrefix     string
	ReleaseTag    string // release tag prefix; the tag is ReleaseTag + version
	RequirePinned bool   // admit only digests already in the host's static approvals
}

var nativeCatalogSpecs = []nativeCatalogSpec{
	{PluginID: "bloem.storage.bookwarehouse", KeyPrefix: "bookwarehouse", ReleaseTag: "bookwarehouse-v"},
	{PluginID: "bloem.storage.s3", KeyPrefix: "s3", ReleaseTag: "s3-v", RequirePinned: true},
}

func nativeCatalogSpecFor(pluginID string) (nativeCatalogSpec, bool) {
	for _, s := range nativeCatalogSpecs {
		if s.PluginID == pluginID {
			return s, true
		}
	}
	return nativeCatalogSpec{}, false
}
```

Change `storageCatalogURL` to `func storageCatalogURL(spec nativeCatalogSpec, raw, version, file string) bool` and its final line to `return u.Path == "/Bloem-Studios/bloem-plugins/releases/download/"+spec.ReleaseTag+version+"/"+file && version != "" && !strings.ContainsAny(version, "/\\?#")`. Change `catalogArtifactKey` to `func catalogArtifactKey(spec nativeCatalogSpec, version, checksum string) string { return spec.KeyPrefix + "-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + "-" + checksum }`.

In `loadCatalog`, inside the record loop, after unmarshalling `m`: `spec, ok := nativeCatalogSpecFor(m.GetPluginId()); if !ok { return errors.New("invalid native catalog release") }`, then `if spec.RequirePinned && !r.pinned[record.Checksum] { continue }`, and replace the existing `m.GetPluginId() != nativeCatalogPlugin` / `catalogArtifactKey(...)` / `storageCatalogURL(...)` checks with the spec-aware forms. Keep every other condition on that line unchanged.

In `RefreshCatalog`, replace `if release.ID != nativeCatalogPlugin { continue }` with `spec, ok := nativeCatalogSpecFor(release.ID); if !ok { continue }`; rename the later `ok` from `binary, ok :=` to `binary, found :=`; add after the checksum validation `if spec.RequirePinned && !r.pinned[binary.Checksum] { continue }`; use `storageCatalogURL(spec, binary.URL, release.Version, ...)`, build `manifestURL` as `"https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + spec.ReleaseTag + release.Version + "/manifest.json"`, and `key := catalogArtifactKey(spec, release.Version, binary.Checksum)`.

In `bloem_storage_registry.go` add field `pinned map[string]bool` to `NativeStorageRegistry`; in `NewNativeStorageRegistry`, after `approvals, err := ...`, build `pinned := make(map[string]bool, len(approvals)); for _, a := range approvals { pinned[a.Checksum] = true }` and set `pinned: pinned` in the struct literal.

In `bloem_storage_management.go` add `Catalog bool \`json:"catalog"\`` to `NativeStorageArtifactView` and in `ApprovedArtifacts` build the view with `r.catalogMu.RLock(); _, isCatalog := r.catalogRecords[key]; r.catalogMu.RUnlock()` (take the lock once outside the loop and copy `catalogRecords` keys), setting `Catalog: isCatalog`.

- [ ] **Step 4: Run tests.** Run: `go build ./... && go test ./internal/plugins -run 'TestNativeCatalog|TestNativeStorage' -count=1`. Expected: PASS. Fix any other call sites the compiler reports for the changed helper signatures.

- [ ] **Step 5: Commit.**

```bash
git add internal/plugins/bloem_storage_catalog.go internal/plugins/bloem_storage_catalog_test.go internal/plugins/bloem_storage_registry.go internal/plugins/bloem_storage_management.go
git commit -m "feat(storage): catalog supports S3 releases pinned to approved digests"
```

---

### Task 2: Artifacts carry the manifest form

**Files:**
- Modify: `internal/plugins/bloem_storage_management.go` (add `Manifest` accessor), `internal/api/handlers/bloem_native_storage_management.go` (`HandleArtifacts`), `internal/api/handlers/bloem_native_storage_contract.go`
- Test: `internal/api/handlers/bloem_native_storage_artifacts_test.go` (new)

**Interfaces:**
- Consumes: `configSchemasToJSON([]*pluginv1.ConfigSchema) []PluginConfigSchemaView` (`internal/api/handlers/plugins.go:1536`), `NativeStorageArtifactView` from Task 1.
- Produces: `plugins.NativeStorageArtifactView.ConfigSchema []*publicv1.ConfigSchema` tagged `json:"-"`; response type `nativeStorageArtifactResponse` with `config_schema` (`[]PluginConfigSchemaView`) embedded fields.

- [ ] **Step 1: Write the failing test.**

```go
package handlers

import (
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/plugins"
	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestNativeStorageArtifactResponseCarriesFormWithoutSecrets(t *testing.T) {
	view := plugins.NativeStorageArtifactView{ArtifactKey: "s3-0.3.2-linux-amd64-x", PluginID: "bloem.storage.s3", Version: "0.3.2", OS: "linux", Arch: "amd64", Catalog: true,
		ConfigSchema: []*publicv1.ConfigSchema{{Key: "connection", Title: "S3 storage connection", JsonSchema: `{"type":"object","properties":{"bucket":{"type":"string"}}}`}}}
	body, err := json.Marshal(nativeStorageArtifactsResponse{Artifacts: nativeStorageArtifactResponses([]plugins.NativeStorageArtifactView{view})})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Artifacts []struct {
			ArtifactKey  string `json:"artifact_key"`
			Catalog      bool   `json:"catalog"`
			ConfigSchema []struct {
				Key        string `json:"key"`
				JSONSchema string `json:"json_schema"`
			} `json:"config_schema"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Artifacts) != 1 || !got.Artifacts[0].Catalog || len(got.Artifacts[0].ConfigSchema) != 1 || got.Artifacts[0].ConfigSchema[0].Key != "connection" {
		t.Fatalf("unexpected artifacts document: %s", body)
	}
}
```

- [ ] **Step 2: Run to verify failure.** `go test ./internal/api/handlers -run TestNativeStorageArtifactResponseCarriesForm -count=1` → FAIL (undefined `ConfigSchema`, `nativeStorageArtifactResponses`).

- [ ] **Step 3: Implement.** In `bloem_storage_management.go` add field `ConfigSchema []*publicv1.ConfigSchema \`json:"-"\`` to the view and set it in `ApprovedArtifacts` from `a.Manifest.GetGlobalConfigSchema()` (check the exact getter name on `publicv1.PluginManifest`; it is the repeated field backing the manifest's `global_config_schema`). In `bloem_native_storage_contract.go` replace `nativeStorageArtifactsResponse` with:

```go
type nativeStorageArtifactResponse struct {
	plugins.NativeStorageArtifactView
	ConfigSchema []PluginConfigSchemaView `json:"config_schema"`
}

type nativeStorageArtifactsResponse struct {
	Artifacts []nativeStorageArtifactResponse `json:"artifacts"`
}

func nativeStorageArtifactResponses(in []plugins.NativeStorageArtifactView) []nativeStorageArtifactResponse {
	out := make([]nativeStorageArtifactResponse, 0, len(in))
	for _, v := range in {
		schema := configSchemasToJSON(v.ConfigSchema)
		if schema == nil {
			schema = []PluginConfigSchemaView{}
		}
		out = append(out, nativeStorageArtifactResponse{NativeStorageArtifactView: v, ConfigSchema: schema})
	}
	return out
}
```

(import `github.com/Silo-Server/silo-server/internal/plugins` in that file if absent). In `HandleArtifacts` change the final write to `nativeStorageWrite(w, 200, nativeStorageArtifactsResponse{nativeStorageArtifactResponses(h.Registry.ApprovedArtifacts())})`.

- [ ] **Step 4: Run.** `go build ./... && go test ./internal/api/handlers -run 'NativeStorage' -count=1` → PASS.

- [ ] **Step 5: Commit.** `git add internal/plugins/bloem_storage_management.go internal/api/handlers && git commit -m "feat(storage): artifacts list carries each plugin's manifest form"`

---

### Task 3: Non-secret connection summary

**Files:**
- Create: `internal/nativestorage/connection_summary.go`, `internal/nativestorage/connection_summary_test.go`, `internal/plugins/bloem_storage_connection.go`, `internal/plugins/bloem_storage_connection_test.go`
- Modify: `internal/nativestorage/source_management.go` (`SourceView`), `internal/nativestorage/source_management_store.go` (`sourceViewTx`)

**Interfaces:**
- Produces: `nativestorage.ConnectionSummary(pluginID string, connection map[string]any) map[string]string`; `(*plugins.NativeStorageRegistry).ConnectionConfig(ctx, installationID int) (map[string]any, error)` (decrypted, never returned to clients); `SourceView.ConnectionSummary map[string]string` (`json:"connection_summary"`, never nil).

- [ ] **Step 1: Write the failing tests.** `connection_summary_test.go`:

```go
package nativestorage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConnectionSummaryS3ExposesOnlyNonSecretFields(t *testing.T) {
	conn := map[string]any{"source_id": "s3_1", "source_name": "Movies", "bucket": "media", "prefix": "movies/", "region": "us-east-1",
		"endpoint": "https://s3.example.com", "revision_mode": "checked-etag", "probe_key": "movies/x.mkv",
		"access_key": "AKIASECRETVALUE", "secret_key": "SECRETKEYVALUE", "session_token": "TOKENVALUE", "state_path": "/var/x"}
	got := ConnectionSummary("bloem.storage.s3", conn)
	want := map[string]string{"source_name": "Movies", "bucket": "media", "prefix": "movies/", "region": "us-east-1",
		"endpoint": "https://s3.example.com", "revision_mode": "checked-etag", "probe_key": "movies/x.mkv"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q want %q", k, got[k], v)
		}
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"AKIASECRETVALUE", "SECRETKEYVALUE", "TOKENVALUE", "/var/x"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("summary leaked %q", secret)
		}
	}
}

func TestConnectionSummaryEmptyPrefixIsPresentAndUnknownPluginIsEmpty(t *testing.T) {
	if got := ConnectionSummary("bloem.storage.s3", map[string]any{"bucket": "b"}); got["prefix"] != "" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got := ConnectionSummary("other.plugin", map[string]any{"bucket": "b"}); got == nil || len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := ConnectionSummary("bloem.storage.bookwarehouse", map[string]any{"base_url": "https://books.example/api", "api_key": "K"}); got["base_url_host"] != "books.example" || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
```

`bloem_storage_connection_test.go` (pure part only; the DB-backed reader is covered by Task 14's integration run):

```go
package plugins

import "testing"

func TestPickConnectionConfigPrefersConnectionKey(t *testing.T) {
	configs := []*RuntimeConfig{{Key: "other", Value: map[string]any{"a": 1}}, {Key: "connection", Value: map[string]any{"bucket": "b"}}}
	if got := pickConnectionConfig(configs); got["bucket"] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := pickConnectionConfig(configs[:1]); got["a"] != 1 {
		t.Fatalf("fallback to first config failed: %v", got)
	}
	if pickConnectionConfig(nil) != nil {
		t.Fatal("expected nil")
	}
}
```

- [ ] **Step 2: Run to verify failure.** `go test ./internal/nativestorage -run TestConnectionSummary -count=1 && go test ./internal/plugins -run TestPickConnectionConfig -count=1` → FAIL (undefined).

- [ ] **Step 3: Implement.** `internal/nativestorage/connection_summary.go`:

```go
package nativestorage

import "net/url"

// ConnectionSummary returns the display-safe fields of a stored plugin
// connection. It is an allowlist per plugin: anything not named here, above all
// credentials and host paths, is never serialized. The result is never nil.
func ConnectionSummary(pluginID string, connection map[string]any) map[string]string {
	out := map[string]string{}
	text := func(key string) string { v, _ := connection[key].(string); return v }
	switch pluginID {
	case "bloem.storage.s3":
		out["prefix"] = text("prefix")
		for _, key := range []string{"source_name", "bucket", "region", "endpoint", "revision_mode", "probe_key"} {
			if v := text(key); v != "" {
				out[key] = v
			}
		}
		if allow, _ := connection["allow_loopback_http"].(bool); allow {
			out["allow_loopback_http"] = "true"
		}
		if len(out) == 1 && out["prefix"] == "" && text("bucket") == "" {
			return map[string]string{}
		}
	case "bloem.storage.bookwarehouse":
		if u, err := url.Parse(text("base_url")); err == nil && u.Hostname() != "" {
			out["base_url_host"] = u.Hostname()
		}
	}
	return out
}
```

Run the tests; if the "prefix present when only bucket given" case yields `len==2` as asserted, remove the trailing `if len(out) == 1 ...` block (it exists only to return `{}` for an entirely empty S3 config; keep it if `ConnectionSummary("bloem.storage.s3", nil)` should be `{}` and add that assertion to the test).

`internal/plugins/bloem_storage_connection.go`:

```go
package plugins

import "context"

func pickConnectionConfig(configs []*RuntimeConfig) map[string]any {
	for _, c := range configs {
		if c != nil && c.Key == "connection" {
			return c.Value
		}
	}
	if len(configs) > 0 && configs[0] != nil {
		return configs[0].Value
	}
	return nil
}

// ConnectionConfig returns the decrypted `connection` runtime config of an
// installation for host-side use only. Callers must never serialize it.
func (r *NativeStorageRegistry) ConnectionConfig(ctx context.Context, installationID int) (map[string]any, error) {
	if r == nil || r.configs == nil {
		return nil, nil
	}
	configs, err := r.configs.ListGlobalConfigs(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return pickConnectionConfig(configs), nil
}
```

In `source_management.go` add `ConnectionSummary map[string]string \`json:"connection_summary"\`` to `SourceView`. In `source_management_store.go`: change `sourceView(...)` to initialize `ConnectionSummary: map[string]string{}`; in `sourceViewTx`, read the final `return sourceView(...)` (lines ~100-125), assign it to `view`, and before returning add:

```go
if source.InstallationID != nil {
	if cfg, e := s.registry.ConnectionConfig(ctx, int(*source.InstallationID)); e == nil {
		view.ConnectionSummary = ConnectionSummary(source.PluginID, cfg)
	}
}
return view, nil
```

Confirm the list path (`ListSources`) also goes through `sourceViewTx` or `sourceView`; if it builds views separately, add the same lines there (`grep -n "sourceView(" internal/nativestorage/*.go`).

- [ ] **Step 4: Run.** `go build ./... && go test ./internal/nativestorage ./internal/plugins -run 'ConnectionSummary|PickConnection' -count=1` → PASS.

- [ ] **Step 5: Commit.** `git add internal/nativestorage internal/plugins && git commit -m "feat(storage): source documents carry a non-secret connection summary"`

---

### Task 4: Catalog discover endpoint (platform only)

**Files:**
- Create: `internal/api/handlers/bloem_native_storage_catalog_discovery.go`, `internal/api/handlers/bloem_native_storage_catalog_discovery_test.go`
- Modify: `internal/api/router_bloem_native_storage_management.go`, `internal/api/handlers/bloem_native_storage_management.go` (capabilities map)

**Interfaces:**
- Consumes: `nativeStorageDiscoverInstallRequest`, `nativeStorageInvalid`, `writeNativeStorageError`, `nativeStorageSourceResponse`, `nativeManagementCatalogUnavailable`, `h.Registry.CatalogBinary`, `h.Sources.DiscoverInstall`.
- Produces: `nativeStorageCatalogDiscoverRequest(cmd *nativestorage.DiscoverInstallCommand, fields map[string]json.RawMessage, scope auth.AdminScope) error`; `HandleCatalogDiscoverInstall`; route `POST /catalog/installations/discover`; capability `s3_catalog_discovery`.

- [ ] **Step 1: Write the failing test.**

```go
package handlers

import (
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

func catalogDiscoverFields(extra map[string]string) map[string]json.RawMessage {
	f := map[string]json.RawMessage{"artifact_key": json.RawMessage(`"s3-0.3.2-linux-amd64-x"`), "provider_source_id": json.RawMessage(`"s3_abc"`),
		"enabled": json.RawMessage(`true`), "config": json.RawMessage(`{"connection":{"bucket":"media"}}`)}
	for k, v := range extra {
		f[k] = json.RawMessage(v)
	}
	return f
}

func TestCatalogDiscoverRequestPlatformOnly(t *testing.T) {
	cmd := nativestorage.DiscoverInstallCommand{ArtifactKey: "s3-0.3.2-linux-amd64-x", ProviderSourceID: "s3_abc", Enabled: true,
		Config: map[string]map[string]any{"connection": {"bucket": "media"}}}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopePlatform); err != nil {
		t.Fatalf("platform request rejected: %v", err)
	}
	err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopeOrganization)
	var typed *catalog.NativeOnboardingError
	if !asNativeError(err, &typed) || typed.Code != "not_found" {
		t.Fatalf("organization scope must be not_found, got %v", err)
	}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"organization_id": "null"}), auth.AdminScopePlatform); err == nil {
		t.Fatal("organization_id must be rejected even when null")
	}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(map[string]string{"root_entry_id": `"x"`}), auth.AdminScopePlatform); err == nil {
		t.Fatal("unknown/explicit root field accepted")
	}
	missing := catalogDiscoverFields(nil)
	delete(missing, "config")
	if err := nativeStorageCatalogDiscoverRequest(&cmd, missing, auth.AdminScopePlatform); err == nil {
		t.Fatal("missing config accepted")
	}
}

func TestCatalogDiscoverRequestRejectsStatePath(t *testing.T) {
	cmd := nativestorage.DiscoverInstallCommand{ArtifactKey: "k", ProviderSourceID: "p", Enabled: true, Config: map[string]map[string]any{"connection": {"state_path": ""}}}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, catalogDiscoverFields(nil), auth.AdminScopePlatform); err == nil {
		t.Fatal("state_path accepted")
	}
}
```

Add the helper once in the test file: `func asNativeError(err error, target **catalog.NativeOnboardingError) bool { return errors.As(err, target) }` (import `errors`).

- [ ] **Step 2: Run to verify failure.** `go test ./internal/api/handlers -run TestCatalogDiscover -count=1` → FAIL (undefined). (If `nativeStorageConfiguration` does not already reject `state_path`, the second test shows it; add the rejection in `nativeStorageCatalogDiscoverRequest`: `if _, present := cmd.Config["connection"]["state_path"]; present { return nativeStorageInvalid() }`.)

- [ ] **Step 3: Implement.** `bloem_native_storage_catalog_discovery.go`:

```go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
)

// nativeStorageCatalogDiscoverRequest validates the JSON body of a catalog
// discovery. Only platform administrators may add an S3 source this way, and
// the body never selects an owner, root, source key or revision.
func nativeStorageCatalogDiscoverRequest(cmd *nativestorage.DiscoverInstallCommand, fields map[string]json.RawMessage, scope auth.AdminScope) error {
	if scope != auth.AdminScopePlatform {
		return &catalog.NativeOnboardingError{Code: "not_found"}
	}
	if _, present := fields["organization_id"]; present {
		return nativeStorageInvalid()
	}
	if err := nativeStorageDiscoverInstallRequest(cmd, fields, scope); err != nil {
		return err
	}
	if _, present := cmd.Config["connection"]["state_path"]; present {
		return nativeStorageInvalid()
	}
	return nil
}

// HandleCatalogDiscoverInstall installs a catalog artifact the host downloads
// itself and discovers the new source's opaque root, as the manual discover does.
func (h *BloemNativeStorageManagementHandler) HandleCatalogDiscoverInstall(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	var cmd nativestorage.DiscoverInstallCommand
	fields, ok := h.command(w, r, &cmd)
	if !ok {
		return
	}
	if err := nativeStorageCatalogDiscoverRequest(&cmd, fields, actor.Scope); err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	if h.Registry == nil {
		writeNativeStorageError(w, nativeManagementCatalogUnavailable(), true)
		return
	}
	binary, err := h.Registry.CatalogBinary(r.Context(), cmd.ArtifactKey)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	cmd.Binary = binary
	source, err := h.Sources.DiscoverInstall(r.Context(), actor, cmd)
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, http.StatusCreated, nativeStorageSourceResponse{source})
}
```

If `auth.AdminScopePlatform` has a different identifier, `grep -n "AdminScope" internal/auth/*.go` and use the platform constant. Router: add `r.Post("/catalog/installations/discover", handler.HandleCatalogDiscoverInstall)` directly after the existing `/catalog/installations` line. Capabilities: in the map literal near `"s3_initial_root_discovery": discoveryReady,` add `"s3_catalog_discovery": discoveryReady && h.Registry != nil,`.

- [ ] **Step 4: Run.** `go build ./... && go test ./internal/api/... -run 'CatalogDiscover|NativeStorage' -count=1` → PASS.

- [ ] **Step 5: Commit.** `git add internal/api && git commit -m "feat(storage): install and discover S3 sources from the catalog (platform scope)"`

---

### Task 5: Credential rotation for in-use S3 sources

**Files:**
- Modify: `internal/plugins/bloem_storage_registry.go` (`replaceConfiguration` rotation flag), `internal/plugins/bloem_storage_connection.go`, `internal/nativestorage/source_management.go`, `internal/api/router_bloem_native_storage_management.go`, `internal/api/handlers/bloem_native_storage_management.go` (capabilities)
- Create: `internal/api/handlers/bloem_native_storage_credentials.go`, `internal/api/handlers/bloem_native_storage_credentials_test.go`
- Test: `internal/plugins/bloem_storage_connection_test.go`

**Interfaces:**
- Produces: `overlayS3Credentials(current map[string]any, c S3Credentials) (map[string]map[string]any, error)`; `type S3Credentials struct{ AccessKey, SecretKey, SessionToken string }`; `(*NativeStorageRegistry).rotateCredentials(ctx, key, owner uuid.UUID, c S3Credentials, expected int64, authorize NativeStorageAuthorizeTx) (int64, error)`; `(*SourceManagement).RotateCredentials(ctx, actor, key uuid.UUID, expected int64, c plugins.S3Credentials) (int64, error)`; route `POST /sources/{source_key}/credentials`; capability `credential_rotation_in_use`.

- [ ] **Step 1: Write the failing tests.** Append to `bloem_storage_connection_test.go`:

```go
func TestOverlayS3CredentialsChangesOnlyCredentialFields(t *testing.T) {
	current := map[string]any{"source_id": "s3_1", "bucket": "media", "prefix": "movies/", "region": "us-east-1", "endpoint": "https://s3.example.com",
		"revision_mode": "checked-etag", "access_key": "OLD", "secret_key": "OLD2", "session_token": "OLD3"}
	next, err := overlayS3Credentials(current, S3Credentials{AccessKey: "NEW", SecretKey: "NEW2"})
	if err != nil {
		t.Fatal(err)
	}
	conn := next["connection"]
	for _, k := range []string{"source_id", "bucket", "prefix", "region", "endpoint", "revision_mode"} {
		if conn[k] != current[k] {
			t.Fatalf("%s changed: %v", k, conn[k])
		}
	}
	if conn["access_key"] != "NEW" || conn["secret_key"] != "NEW2" {
		t.Fatalf("credentials not replaced: %v", conn)
	}
	if _, kept := conn["session_token"]; kept {
		t.Fatal("empty session token must clear the old one")
	}
	if current["access_key"] != "OLD" {
		t.Fatal("input map was mutated")
	}
}

func TestOverlayS3CredentialsRejectsMissingKeysAndMissingConnection(t *testing.T) {
	if _, err := overlayS3Credentials(map[string]any{"bucket": "b"}, S3Credentials{AccessKey: "A"}); err == nil {
		t.Fatal("secret key required")
	}
	if _, err := overlayS3Credentials(nil, S3Credentials{AccessKey: "A", SecretKey: "B"}); err == nil {
		t.Fatal("source without stored connection accepted")
	}
}
```

`bloem_native_storage_credentials_test.go`:

```go
package handlers

import "testing"

func TestCredentialRotationBodyValidation(t *testing.T) {
	good := `{"expected_revision":3,"access_key":"AK","secret_key":"SK"}`
	if _, err := nativeStorageCredentialsDecode([]byte(good)); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}
	for name, body := range map[string]string{
		"missing revision": `{"access_key":"AK","secret_key":"SK"}`,
		"zero revision":    `{"expected_revision":0,"access_key":"AK","secret_key":"SK"}`,
		"missing secret":   `{"expected_revision":1,"access_key":"AK"}`,
		"unknown field":    `{"expected_revision":1,"access_key":"AK","secret_key":"SK","bucket":"other"}`,
		"control char":     "{\"expected_revision\":1,\"access_key\":\"A\\u0000K\",\"secret_key\":\"SK\"}",
	} {
		if _, err := nativeStorageCredentialsDecode([]byte(body)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure.** `go test ./internal/plugins -run Overlay -count=1 && go test ./internal/api/handlers -run CredentialRotationBody -count=1` → FAIL (undefined).

- [ ] **Step 3: Implement.** In `bloem_storage_connection.go` add:

```go
import ("errors"; "unicode")  // merge with existing imports

// S3Credentials are the only fields a rotation may change.
type S3Credentials struct{ AccessKey, SecretKey, SessionToken string }

// overlayS3Credentials copies the stored connection and replaces only the three
// credential fields, so every other setting is unchanged by construction.
func overlayS3Credentials(current map[string]any, c S3Credentials) (map[string]map[string]any, error) {
	if current == nil || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("credential rotation requires a stored connection and both keys")
	}
	conn := make(map[string]any, len(current)+3)
	for k, v := range current {
		conn[k] = v
	}
	delete(conn, "session_token")
	conn["access_key"], conn["secret_key"] = c.AccessKey, c.SecretKey
	if c.SessionToken != "" {
		conn["session_token"] = c.SessionToken
	}
	return map[string]map[string]any{"connection": conn}, nil
}
```

(drop the unused `unicode` import; add it only if used.) In `bloem_storage_registry.go` rename the existing `replaceConfiguration` body to `replaceConfigurationMode(ctx, key, owner, config, expected, authorize, rotation bool)` and make `replaceConfiguration(...)` call it with `false`; inside, wrap only the `nativeManagementEmptyNamespaceTx(...)` call as `if !rotation { if err = nativeManagementEmptyNamespaceTx(...); err != nil { return 0, err } }`. Add to `bloem_storage_connection.go`:

```go
// rotateCredentials replaces the stored S3 credentials of an in-use source. It
// reads the stored connection, overlays only the credential fields and applies
// it through the normal replacement transaction with the empty-namespace rule
// waived. The caller's expected revision fences concurrent edits.
func (r *NativeStorageRegistry) rotateCredentials(ctx context.Context, key, owner uuid.UUID, c S3Credentials, expected int64, authorize NativeStorageAuthorizeTx) (int64, error) {
	var installationID int
	if err := r.pool.QueryRow(ctx, `SELECT installation_id FROM bloem_storage_sources WHERE key=$1 AND owner_id=$2 AND plugin_id='bloem.storage.s3'`, key, owner).Scan(&installationID); err != nil {
		return 0, storagesource.ErrSourceUnavailable
	}
	current, err := r.ConnectionConfig(ctx, installationID)
	if err != nil {
		return 0, err
	}
	next, err := overlayS3Credentials(current, c)
	if err != nil {
		return 0, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return r.replaceConfigurationMode(ctx, key, owner, next, &expected, authorize, true)
}

func (r *NativeStorageRegistry) RotateCredentials(ctx context.Context, key, owner uuid.UUID, c S3Credentials, expected int64, authorize NativeStorageAuthorizeTx) (int64, error) {
	return r.rotateCredentials(ctx, key, owner, c, expected, authorize)
}
```

(import `github.com/google/uuid`, `catalog`, `storagesource` as the sibling files do). In `source_management.go`, add `RotateCredentials` mirroring `ReplaceConfiguration` (lines ~189-212): copy that method, rename, take `c plugins.S3Credentials`, and call `s.registry.RotateCredentials(ctx, owner, ..., c, expected, authorize)` in place of `ReplaceConfiguration`, keeping its `preflightMutation`/`sourceMutationAuthorizer` usage unchanged. Handler `bloem_native_storage_credentials.go`:

```go
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"unicode"

	"github.com/Silo-Server/silo-server/internal/plugins"
)

type nativeStorageCredentialsCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	AccessKey        string `json:"access_key"`
	SecretKey        string `json:"secret_key"`
	SessionToken     string `json:"session_token"`
}

func nativeStorageCredentialsDecode(data []byte) (nativeStorageCredentialsCommand, error) {
	var cmd nativeStorageCredentialsCommand
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil || dec.More() {
		return cmd, nativeStorageInvalid()
	}
	if cmd.ExpectedRevision <= 0 || cmd.AccessKey == "" || cmd.SecretKey == "" {
		return cmd, nativeStorageInvalid()
	}
	for _, v := range []string{cmd.AccessKey, cmd.SecretKey, cmd.SessionToken} {
		for _, r := range v {
			if unicode.IsControl(r) {
				return cmd, nativeStorageInvalid()
			}
		}
	}
	return cmd, nil
}

func (h *BloemNativeStorageManagementHandler) HandleRotateCredentials(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	key, ok := nativeStoragePathUUID(w, r, "source_key")
	if !ok {
		return
	}
	var raw nativeStorageCredentialsCommand
	if _, ok = h.command(w, r, &raw); !ok {
		return
	}
	cmd, err := nativeStorageCredentialsDecode(mustJSON(raw))
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	if !h.sourceAvailable(w) {
		return
	}
	revision, err := h.Sources.RotateCredentials(r.Context(), actor, key, cmd.ExpectedRevision, plugins.S3Credentials{AccessKey: cmd.AccessKey, SecretKey: cmd.SecretKey, SessionToken: cmd.SessionToken})
	if err != nil {
		writeNativeStorageError(w, err, true)
		return
	}
	nativeStorageWrite(w, 200, nativeStorageConfigurationResponse{SourceKey: key, ConfigurationRevision: revision})
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
```

Unknown fields are already rejected by `h.command` (`nativeStorageJSON` decodes strictly), so `nativeStorageCredentialsDecode` is the second, test-pinned layer; check `nativeStorageConfigurationResponse` field names in `bloem_native_storage_contract.go` and use them. Router: `r.Post("/sources/{source_key}/credentials", handler.HandleRotateCredentials)`. Capability: add `"credential_rotation_in_use": ready,` next to `"configuration_replace_unbound": ready,`.

- [ ] **Step 4: Run.** `go build ./... && go test ./internal/plugins ./internal/api/handlers ./internal/nativestorage -run 'Overlay|CredentialRotation|NativeStorage' -count=1` → PASS.

- [ ] **Step 5: Commit.** `git add -A internal && git commit -m "feat(storage): rotate S3 credentials on in-use sources"`

---

### Task 6: Contracts and docs

**Files:**
- Modify: `internal/apiv2/bloem_native_storage_document.go`, `docs/bloem-api-reference.md`, `docs/architecture/bloem-native-storage.md`, `contracts/api/bloem/v1/openapi.json`

- [ ] **Step 1:** In `bloem_native_storage_document.go` add document-only declarations for `POST /catalog/installations/discover` (body: `artifact_key`, `provider_source_id`, `enabled`, `config`; platform scope only; responses `201`, `400`, `404`, `422`, `503`), `POST /sources/{source_key}/credentials` (body `expected_revision`, `access_key`, `secret_key`, `session_token?`; `200 {source_key, configuration_revision}`), the `connection_summary` property on the source schema, `catalog` and `config_schema` on artifacts, and the capabilities `s3_catalog_discovery`, `credential_rotation_in_use`. Follow the neighboring declarations in that file exactly.
- [ ] **Step 2:** Regenerate: `make bloem-openapi` then `make verify-bloem-openapi`. Expected: verify passes; `git diff contracts/api/bloem/v1/openapi.json` shows only the additions above.
- [ ] **Step 3:** Add the two operations to the operations table in `docs/bloem-api-reference.md` and a short "Catalog installs" and "Credential rotation" subsection describing the platform-only rule, the pinned-digest rule for S3, and that rotation overlays only the three credential fields. Update `docs/architecture/bloem-native-storage.md` ("Operations" section) with the same two paragraphs.
- [ ] **Step 4: Commit.** `git add internal/apiv2 docs contracts && git commit -m "docs(storage): document catalog discovery, connection summary and key rotation"`

---

### Task 7: Web types and hooks

**Files:**
- Modify: `web/src/hooks/queries/admin/storageSources.ts`
- Test: `web/src/hooks/queries/admin/storageSources.test.ts` (create if absent)

**Interfaces:**
- Produces (exports): `StorageSource.connection_summary: Record<string,string>`; `StorageArtifact.catalog: boolean`, `StorageArtifact.config_schema: PluginConfigSchema[]`; `useStorageScope` (now exported); `useDiscoverStorageSource()` mutation `{artifactKey, providerSourceId, config}`; `useReplaceStorageConfiguration()` mutation `{source, config}`; `useRotateStorageCredentials()` mutation `{source, accessKey, secretKey, sessionToken?}`; `useDisableStorageSource()` and `useRemoveStorageSource()` mutations `{source}`; `useTestStorageSource()` mutation `{source}` resolving `{ok: boolean; message: string}`.

- [ ] **Step 1: Failing test.** Create `storageSources.test.ts` testing the pure helper `storageRequestBody` you add in step 3:

```ts
import { describe, expect, it } from "vitest";
import { discoverBody, rotateBody } from "./storageSources";

describe("storage source request bodies", () => {
  it("discover sends the catalog contract and no state path or owner", () => {
    const body = discoverBody({
      artifactKey: "s3-0.3.2-linux-amd64-x",
      providerSourceId: "s3_abc",
      config: { connection: { source_id: "s3_abc", bucket: "media" } },
    });
    expect(body).toEqual({
      artifact_key: "s3-0.3.2-linux-amd64-x",
      provider_source_id: "s3_abc",
      enabled: true,
      config: { connection: { source_id: "s3_abc", bucket: "media" } },
    });
  });
  it("rotate sends only the revision and credentials", () => {
    expect(rotateBody({ revision: 4, accessKey: "AK", secretKey: "SK", sessionToken: "" })).toEqual({
      expected_revision: 4,
      access_key: "AK",
      secret_key: "SK",
    });
    expect(rotateBody({ revision: 4, accessKey: "AK", secretKey: "SK", sessionToken: "T" })).toHaveProperty(
      "session_token",
      "T",
    );
  });
});
```

- [ ] **Step 2:** `cd web && npx vitest run src/hooks/queries/admin/storageSources.test.ts` → FAIL (exports missing).
- [ ] **Step 3: Implement.** Add the interface fields; export `useStorageScope`; add

```ts
export function discoverBody(input: { artifactKey: string; providerSourceId: string; config: Record<string, Record<string, unknown>> }) {
  return { artifact_key: input.artifactKey, provider_source_id: input.providerSourceId, enabled: true, config: input.config };
}
export function rotateBody(input: { revision: number; accessKey: string; secretKey: string; sessionToken: string }) {
  return {
    expected_revision: input.revision,
    access_key: input.accessKey,
    secret_key: input.secretKey,
    ...(input.sessionToken ? { session_token: input.sessionToken } : {}),
  };
}
```

and the six mutations modeled on `useInstallStorageSource` (same `adminV2Api(`/${scope}/native-storage/...`, {method:"POST", body: JSON.stringify(...)})` shape, `toast` on error with the server's message, `invalidateQueries({queryKey: adminV2QueryKey(key, "native-storage", "sources")})` on success): discover → `POST catalog/installations/discover`; replace → `PUT sources/{source_key}/configuration` with `{expected_revision: source.configuration_revision, config}`; rotate → `POST sources/{source_key}/credentials`; disable → `POST installations/{installation_id}/disable` with `{source_key, expected_revision}`; remove → `DELETE installations/{installation_id}` with the same body; test → `GET sources/{source_key}/browse?limit=1` resolving `{ok:true,message:"Connection works"}` or `{ok:false,message}` (catch the error; never throw). Read `useUpgradeStorageSource` in the same file for the exact fetch/option conventions before writing them.
- [ ] **Step 4:** rerun the vitest file → PASS; `npx tsc --noEmit -p .` → no errors.
- [ ] **Step 5: Commit.** `git add web/src/hooks && git commit -m "feat(web): storage source hooks for discover, edit, rotate, disable, remove, test"`

---

### Task 8: Add-source dialog (manifest-driven)

**Files:**
- Create: `web/src/components/admin/plugins/storageSchema.ts`, `storageSchema.test.ts`, `AddStorageSourceDialog.tsx`, `AddStorageSourceDialog.test.tsx`
- Modify: `web/src/components/admin/plugins/StorageSourcesPanel.tsx`

**Interfaces:**
- Consumes: `PluginConfigForm` props (`schema`, `onSave(key, value, clearSecrets)`, `isSaving`, `bare`, `idPrefix`), `StorageArtifact`, `useDiscoverStorageSource`, `useStorageScope`.
- Produces: `pruneConfigSchema(schema: PluginConfigSchema, hidden: string[]): PluginConfigSchema`; `validateS3Connection(value: Record<string, unknown>): Record<string, string>` (field → message); `newProviderSourceId(prefix: string): string`; component `AddStorageSourceDialog({ artifacts?: StorageArtifact[]; sources: StorageSource[]; open: boolean; onOpenChange(open: boolean): void; error?: unknown; onRetry?(): void })`.

- [ ] **Step 1: Failing tests.** `storageSchema.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { pruneConfigSchema, validateS3Connection, newProviderSourceId } from "./storageSchema";

const schema = {
  key: "connection",
  title: "S3 storage connection",
  required: true,
  json_schema: JSON.stringify({
    type: "object",
    properties: { source_id: { type: "string" }, bucket: { type: "string" }, state_path: { type: "string" }, probe_key: { type: "string" } },
  }),
  admin_form: {
    fields: [
      { key: "source_id", label: "Source ID", control: "TEXT", required: false, secret: false, multiline: false },
      { key: "bucket", label: "Bucket", control: "TEXT", required: true, secret: false, multiline: false },
      { key: "state_path", label: "State path", control: "TEXT", required: false, secret: false, multiline: false },
    ],
  },
} as const;

describe("pruneConfigSchema", () => {
  it("removes hidden fields from both the JSON schema and the admin form", () => {
    const out = pruneConfigSchema(schema as never, ["source_id", "state_path", "probe_key"]);
    expect(Object.keys(JSON.parse(out.json_schema).properties)).toEqual(["bucket"]);
    expect(out.admin_form?.fields.map((f) => f.key)).toEqual(["bucket"]);
  });
});

describe("validateS3Connection", () => {
  it("accepts empty or slash-terminated prefixes and https endpoints", () => {
    expect(validateS3Connection({ bucket: "media", prefix: "", endpoint: "https://s3.example.com" })).toEqual({});
    expect(validateS3Connection({ bucket: "media", prefix: "movies/", endpoint: "https://s3.example.com" })).toEqual({});
  });
  it("flags a prefix without a trailing slash or with a leading slash", () => {
    expect(validateS3Connection({ bucket: "media", prefix: "movies" }).prefix).toMatch(/end with/);
    expect(validateS3Connection({ bucket: "media", prefix: "/movies/" }).prefix).toMatch(/start/);
  });
  it("flags http, paths and embedded credentials in the endpoint", () => {
    expect(validateS3Connection({ bucket: "media", endpoint: "http://s3.example.com" }).endpoint).toMatch(/https/);
    expect(validateS3Connection({ bucket: "media", endpoint: "https://s3.example.com/x" }).endpoint).toMatch(/path/);
    expect(validateS3Connection({ bucket: "media", endpoint: "https://u:p@s3.example.com" }).endpoint).toMatch(/credentials/);
  });
  it("requires a bucket and valid-looking bucket name", () => {
    expect(validateS3Connection({}).bucket).toBeTruthy();
    expect(validateS3Connection({ bucket: "A_B" }).bucket).toBeTruthy();
  });
});

describe("newProviderSourceId", () => {
  it("matches the plugin source id pattern", () => {
    expect(newProviderSourceId("s3")).toMatch(/^s3_[a-f0-9]{32}$/);
  });
});
```

`AddStorageSourceDialog.test.tsx` (the hooks module is replaced wholesale with `vi.mock`, exactly like `StorageSourcesPanel.test.tsx`):

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { AddStorageSourceDialog } from "./AddStorageSourceDialog";

window.HTMLElement.prototype.scrollIntoView ??= () => {};

const discover = vi.fn();
const install = vi.fn();
let scope: "platform" | "organization" = "platform";

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useDiscoverStorageSource: () => ({ mutate: discover, isPending: false }),
  useInstallStorageSource: () => ({ mutate: install, isPending: false }),
  useStorageScope: () => ({ active: {}, scope, key: scope }),
}));

const field = (key: string, label: string, control = "TEXT", secret = false) => ({
  key, label, control, required: false, secret, multiline: false,
});
const s3Schema = {
  key: "connection",
  title: "S3 storage connection",
  required: true,
  json_schema: JSON.stringify({
    type: "object",
    properties: {
      source_id: { type: "string" }, source_name: { type: "string" }, bucket: { type: "string" },
      prefix: { type: "string" }, region: { type: "string" }, endpoint: { type: "string" },
      access_key: { type: "string" }, secret_key: { type: "string" }, state_path: { type: "string" },
    },
  }),
  admin_form: {
    fields: [
      field("source_id", "Source ID"), field("source_name", "Source name"), field("bucket", "Bucket"),
      field("prefix", "Prefix"), field("region", "Region"), field("endpoint", "S3 endpoint"),
      field("access_key", "Access key", "PASSWORD", true), field("secret_key", "Secret key", "PASSWORD", true),
      field("state_path", "State path"),
    ],
  },
};
const artifacts: StorageArtifact[] = [
  { artifact_key: "s3-0.3.2-linux-amd64-x", plugin_id: "bloem.storage.s3", version: "0.3.2", os: "linux", arch: "amd64", catalog: true, config_schema: [s3Schema as never] },
  { artifact_key: "bookwarehouse-0.2.0", plugin_id: "bloem.storage.bookwarehouse", version: "0.2.0", os: "linux", arch: "amd64", catalog: true, config_schema: [] },
];
const sources: StorageSource[] = [];

function open(props: Partial<React.ComponentProps<typeof AddStorageSourceDialog>> = {}) {
  render(<AddStorageSourceDialog artifacts={artifacts} sources={sources} open onOpenChange={() => {}} {...props} />);
}
function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("AddStorageSourceDialog", () => {
  beforeEach(() => {
    discover.mockReset();
    install.mockReset();
    scope = "platform";
  });

  it("offers S3 and Bookwarehouse to platform admins", () => {
    open();
    expect(screen.getByRole("button", { name: /^S3$/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
  });

  it("does not offer S3 in organization scope", () => {
    scope = "organization";
    open();
    expect(screen.queryByRole("button", { name: /^S3$/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install Bookwarehouse" })).toBeInTheDocument();
  });

  it("hides the source id and state path and masks the keys", () => {
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    expect(screen.getByLabelText("Bucket")).toBeInTheDocument();
    expect(screen.queryByLabelText("Source ID")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("State path")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Access key")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("Secret key")).toHaveAttribute("type", "password");
  });

  it("blocks a prefix without a trailing slash before calling the API", () => {
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media"); fill("Prefix", "movies");
    fill("Access key", "AK"); fill("Secret key", "SK");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(screen.getByRole("alert")).toHaveTextContent(/end with a slash/);
    expect(discover).not.toHaveBeenCalled();
  });

  it("discovers with matching ids, no state path, and clears the keys afterwards", () => {
    discover.mockImplementation((_input: unknown, opts: { onSuccess?: () => void }) => opts.onSuccess?.());
    open();
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media"); fill("Prefix", "movies/"); fill("Region", "us-east-1");
    fill("S3 endpoint", "https://s3.example.com"); fill("Access key", "AK"); fill("Secret key", "SK");
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    const input = discover.mock.calls[0][0] as { artifactKey: string; providerSourceId: string; config: { connection: Record<string, unknown> } };
    expect(input.artifactKey).toBe("s3-0.3.2-linux-amd64-x");
    expect(input.providerSourceId).toMatch(/^s3_[a-f0-9]{32}$/);
    expect(input.config.connection.source_id).toBe(input.providerSourceId);
    expect(input.config.connection).not.toHaveProperty("state_path");
    expect(input.config.connection).toMatchObject({ bucket: "media", prefix: "movies/", access_key: "AK", secret_key: "SK" });
  });

  it("warns about a duplicate bucket and prefix but still allows saving", () => {
    const existing = { ...({} as StorageSource), plugin_id: "bloem.storage.s3", connection_summary: { bucket: "media", prefix: "movies/" } } as StorageSource;
    open({ sources: [existing] });
    fireEvent.click(screen.getByRole("button", { name: /^S3$/ }));
    fill("Bucket", "media"); fill("Prefix", "movies/");
    expect(screen.getByText(/A source with this bucket and prefix already exists/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save/i })).toBeEnabled();
  });

  it("shows a retryable message when the plugin catalog is unavailable", () => {
    const retry = vi.fn();
    open({ artifacts: undefined, error: new Error("boom"), onRetry: retry });
    expect(screen.getByText(/plugin catalog unavailable/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(retry).toHaveBeenCalled();
  });
});
```

The `Save` button text comes from `PluginConfigForm`; if its label is not "Save", change `/save/i` in this file to the label it renders (read `PluginConfigForm.tsx` lines 100-235).
- [ ] **Step 2:** `npx vitest run src/components/admin/plugins/storageSchema.test.ts src/components/admin/plugins/AddStorageSourceDialog.test.tsx` → FAIL (modules missing).
- [ ] **Step 3: Implement.** `storageSchema.ts`:

```ts
import type { PluginConfigSchema } from "@/api/types";

export function pruneConfigSchema(schema: PluginConfigSchema, hidden: string[]): PluginConfigSchema {
  const drop = new Set(hidden);
  let jsonSchema = schema.json_schema;
  try {
    const parsed = JSON.parse(schema.json_schema) as { properties?: Record<string, unknown>; required?: string[] };
    if (parsed.properties) {
      parsed.properties = Object.fromEntries(Object.entries(parsed.properties).filter(([k]) => !drop.has(k)));
      if (parsed.required) parsed.required = parsed.required.filter((k) => !drop.has(k));
      jsonSchema = JSON.stringify(parsed);
    }
  } catch {
    /* keep the original schema text */
  }
  return {
    ...schema,
    json_schema: jsonSchema,
    admin_form: schema.admin_form
      ? { ...schema.admin_form, fields: schema.admin_form.fields.filter((f) => !drop.has(f.key)) }
      : undefined,
  };
}

export function validateS3Connection(value: Record<string, unknown>): Record<string, string> {
  const errors: Record<string, string> = {};
  const text = (k: string) => (typeof value[k] === "string" ? (value[k] as string) : "");
  const bucket = text("bucket");
  if (!bucket) errors.bucket = "Enter the bucket name.";
  else if (!/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(bucket))
    errors.bucket = "Bucket names use 3–63 lowercase letters, digits, dots and hyphens.";
  const prefix = text("prefix");
  if (prefix.startsWith("/")) errors.prefix = "The prefix must not start with a slash.";
  else if (prefix !== "" && !prefix.endsWith("/")) errors.prefix = "The prefix must end with a slash, for example movies/.";
  const endpoint = text("endpoint");
  if (endpoint) {
    try {
      const u = new URL(endpoint);
      if (u.protocol !== "https:") errors.endpoint = "The endpoint must use https.";
      else if (u.username || u.password) errors.endpoint = "The endpoint must not contain credentials.";
      else if (u.pathname !== "/" && u.pathname !== "") errors.endpoint = "The endpoint must not include a path.";
    } catch {
      errors.endpoint = "Enter a full URL such as https://s3.example.com.";
    }
  }
  return errors;
}

export function newProviderSourceId(prefix: string): string {
  return `${prefix}_${crypto.randomUUID().replaceAll("-", "")}`;
}
```

`AddStorageSourceDialog.tsx`: a `Dialog` (reuse `@/components/ui/dialog` as `StorageFolderBrowser.tsx` does) that (1) lists the plugins from `artifacts.filter(a => a.catalog)` grouped by `plugin_id`, taking the highest `version` per plugin (same sort as the existing `InstallForm`), hiding `bloem.storage.s3` when `scope !== "platform"`; (2) after a choice, for S3 renders `<PluginConfigForm bare idPrefix="s3-add" schema={pruneConfigSchema(artifact.config_schema[0], ["source_id","state_path","probe_key","allow_loopback_http","session_token"])} isSaving={discover.isPending} onSave={...} />`; `onSave(key, value)` runs `validateS3Connection(value)`, shows any errors in an inline `role="alert"` list keyed by field label and returns without calling the API, else builds `const id = newProviderSourceId("s3")`, calls `discover.mutate({ artifactKey: artifact.artifact_key, providerSourceId: id, config: { [key]: { ...value, source_id: id } } }, { onSuccess: close })`; (3) shows the duplicate warning computed from `sources.filter(s => s.plugin_id === "bloem.storage.s3")` comparing `connection_summary.bucket`/`prefix`; (4) for Bookwarehouse keeps the existing one-click install path (move `InstallForm`'s button logic in unchanged); (5) when `artifacts` is `undefined` with `isError` shows `plugin catalog unavailable` and a Retry button calling `refetch`. In `StorageSourcesPanel.test.tsx` add `useDiscoverStorageSource`, `useStorageScope`, `useReplaceStorageConfiguration`, `useRotateStorageCredentials`, `useDisableStorageSource`, `useRemoveStorageSource` and `useTestStorageSource` stubs (each `() => ({ mutate: vi.fn(), isPending: false })`, and `useStorageScope: () => ({ active: {}, scope: "platform", key: "platform" })`) to the `vi.mock` factory, because that factory replaces the whole module and the panel now imports them; keep its three existing tests passing (the "Install Bookwarehouse" button must still appear after clicking "Add source"). In `StorageSourcesPanel.tsx` replace the `installing`/`InstallForm` usage with `<AddStorageSourceDialog .../>` opened by the existing "Add source" button; pass `useStorageArtifacts` data including `refetch`/`isError` (extend the hook's return usage, not its signature).
- [ ] **Step 4:** rerun both test files → PASS; `npx tsc --noEmit -p .` and `npx eslint src/components/admin/plugins` → clean.
- [ ] **Step 5: Commit.** `git add web/src/components/admin/plugins && git commit -m "feat(web): add S3 and Bookwarehouse sources from the manifest-driven dialog"`

---

### Task 9: Source rows — summary, test, edit, rotate keys, disable, remove

**Files:**
- Create: `web/src/components/admin/plugins/StorageSourceRow.tsx`, `StorageSourceRow.test.tsx`
- Modify: `web/src/components/admin/plugins/StorageSourcesPanel.tsx`

**Interfaces:**
- Consumes: hooks from Task 7; `pruneConfigSchema`, `validateS3Connection` from Task 8; `PluginConfigForm`.
- Produces: `StorageSourceRow({ source, artifact, inUse })`, where `inUse: boolean` is true when a library uses the source (derive in the panel from the existing library-locations data if available; if the panel has no such data, treat "in use" as unknown and let the server's `configuration_namespace_unverified` (409) error switch the row to the rotate-only view with the message "This source is used by a library; only the keys can be changed.").

- [ ] **Step 1: Failing tests.** `StorageSourceRow.test.tsx` uses the same `vi.mock` pattern (mock factory with the seven new hooks as `{ mutate: <vi.fn>, isPending: false }`, `useTestStorageSource` returning `{ mutate: testSource, isPending: false }`) and the same `field`/`s3Schema` helpers as the dialog test (copy them into this file). Cases, each with its assertions:

```tsx
const s3Source = {
  source_key: "10000000-0000-0000-0000-000000000001", owner_kind: "platform", installation_id: 33,
  plugin_id: "bloem.storage.s3", provider_source_id: "s3_abc", root_entry_id: "root",
  configuration_revision: 5, enabled: true, state: "attached", configured: true,
  connection_summary: { source_name: "Movies", bucket: "media", prefix: "movies/", region: "us-east-1", endpoint: "https://s3.example.com" },
} as StorageSource;

it("shows the non-secret summary and nothing secret", () => {
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  expect(screen.getByText(/media/)).toBeInTheDocument();
  expect(screen.getByText(/movies\//)).toBeInTheDocument();
  expect(screen.getByText(/s3\.example\.com/)).toBeInTheDocument();
  expect(document.body.textContent).not.toMatch(/access_key|secret_key|AKIA/);
});

it("tests the connection by browsing the root", () => {
  testSource.mockImplementation((_s: unknown, o: { onSuccess?: (r: { ok: boolean; message: string }) => void }) => o.onSuccess?.({ ok: true, message: "Connection works" }));
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  fireEvent.click(screen.getByRole("button", { name: "Test" }));
  expect(testSource).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
  expect(screen.getByText("Connection works")).toBeInTheDocument();
});

it("prefills the edit form from the summary and requires the keys again", () => {
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  expect(screen.getByLabelText("Bucket")).toHaveValue("media");
  expect(screen.getByLabelText("Prefix")).toHaveValue("movies/");
  expect(screen.getByLabelText("Access key")).toHaveValue("");
  fireEvent.click(screen.getByRole("button", { name: /save/i }));
  expect(screen.getByRole("alert")).toHaveTextContent(/Enter the keys again to save/);
  expect(replaceConfig).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
  fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
  fireEvent.click(screen.getByRole("button", { name: /save/i }));
  const call = replaceConfig.mock.calls[0][0] as { source: StorageSource; config: { connection: Record<string, unknown> } };
  expect(call.source).toBe(s3Source);
  expect(call.config.connection).toMatchObject({ source_id: "s3_abc", bucket: "media", prefix: "movies/", access_key: "AK", secret_key: "SK" });
});

it("switches to rotate-only when the server says the source is in use", () => {
  replaceConfig.mockImplementation((_i: unknown, o: { onError?: (e: { code: string }) => void }) => o.onError?.({ code: "configuration_namespace_unverified" }));
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "AK" } });
  fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "SK" } });
  fireEvent.click(screen.getByRole("button", { name: /save/i }));
  expect(screen.getByText(/used by a library; only the keys can be changed/i)).toBeInTheDocument();
  expect(screen.queryByLabelText("Bucket")).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "NEWAK" } });
  fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "NEWSK" } });
  fireEvent.click(screen.getByRole("button", { name: "Rotate keys" }));
  expect(rotate).toHaveBeenCalledWith({ source: s3Source, accessKey: "NEWAK", secretKey: "NEWSK", sessionToken: "" }, expect.any(Object));
});

it("confirms before removing and says the source is retained", () => {
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(screen.getByText(/retained/i)).toBeInTheDocument();
  expect(remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: /confirm remove/i }));
  expect(remove).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
});

it("disables the source through the disable call", () => {
  render(<ul><StorageSourceRow source={s3Source} artifact={s3Artifact} /></ul>);
  fireEvent.click(screen.getByRole("button", { name: "Disable" }));
  expect(disable).toHaveBeenCalledWith({ source: s3Source }, expect.any(Object));
});
```

Define `testSource`, `replaceConfig`, `rotate`, `remove`, `disable` as top-level `vi.fn()`s reset in `beforeEach`, `s3Artifact` as the Task 8 S3 artifact, and keep the existing Bookwarehouse row cases in `StorageSourcesPanel.test.tsx` unchanged (they cover the Bookwarehouse URL/API-key form, which the row component keeps).
- [ ] **Step 2:** `npx vitest run src/components/admin/plugins/StorageSourceRow.test.tsx` → FAIL.
- [ ] **Step 3: Implement.** Move the per-source JSX out of `StorageSourcesPanel.tsx` into `StorageSourceRow.tsx`, keep `ConnectionForm` for Bookwarehouse unchanged, and add the S3 branch: summary lines, action buttons (Test, Edit, Rotate keys, Disable, Remove, plus the existing Upgrade control), the prefilled edit form built from `pruneConfigSchema(artifact.config_schema[0], ["source_id","state_path","allow_loopback_http","session_token"])` with `value` taken from the summary (`source_name`, `bucket`, `prefix`, `region`, `endpoint`, `revision_mode`, `probe_key`), and the save handler that runs `validateS3Connection`, requires non-empty `access_key` and `secret_key`, sets `source_id: source.provider_source_id` in the posted `connection` object, and calls `useReplaceStorageConfiguration`. Use `source.provider_source_id` as `source_id` so the plugin namespace stays bound to the same source. Show the rotate-only view when the replace mutation fails with code `configuration_namespace_unverified` (the server error envelope's `code`; read how `adminV2Api` surfaces it in `web/src/api/adminV2Client.ts`).
- [ ] **Step 4:** tests PASS; `npx tsc --noEmit -p .`; `npx eslint src/components/admin/plugins` clean.
- [ ] **Step 5: Commit.** `git add web/src/components/admin/plugins && git commit -m "feat(web): manage storage sources — test, edit, rotate keys, disable, remove"`

---

### Task 10: Picker explains an empty root

**Files:**
- Modify: `web/src/bloem/library-locations/StorageFolderBrowser.tsx`, `StorageFolderBrowser.test.tsx`, `web/src/bloem/library-locations/types.ts` (`StorageSource` re-export or field)

- [ ] **Step 1: Failing test** appended to `StorageFolderBrowser.test.tsx`, reusing the file's existing `render`/`api` helpers: render the browser with `source.connection_summary = { prefix: "movies/10s/" }` and an `api.browse` resolving `{ current: {name:"", logical_path:"", selection_ref:"root"}, breadcrumbs: [], directories: [], next_cursor: "" }`; assert the text `This source starts at movies/10s/ and has no subfolders. Use the current folder, or edit the source's prefix.` is shown. A second case with `connection_summary = { prefix: "" }` asserts the older text `No subfolders. You can use the current folder.` still appears (bucket-root source), and a third with no `connection_summary` at all shows the older text.
- [ ] **Step 2:** `npx vitest run src/bloem/library-locations/StorageFolderBrowser.test.tsx` → FAIL.
- [ ] **Step 3: Implement.** In the empty-state block replace the paragraph with:

```tsx
{page && !loading && !page.directories.length && !page.next_cursor && (
  <p className="text-muted-foreground p-3 text-sm">
    {source.connection_summary?.prefix && !page.breadcrumbs.length
      ? `This source starts at ${source.connection_summary.prefix} and has no subfolders. Use the current folder, or edit the source's prefix.`
      : "No subfolders. You can use the current folder."}
  </p>
)}
```

Ensure the `StorageSource` type used by this component (`./types`) exposes optional `connection_summary?: Record<string, string>`.
- [ ] **Step 4:** test PASS; `npx tsc --noEmit -p .`.
- [ ] **Step 5: Commit.** `git add web/src/bloem && git commit -m "feat(web): picker names the source prefix when its root has no folders"`

---

### Task 11: Plugin regression tests (separate repo)

**Files:** in a fresh worktree of `Bloem-Studios/bloem-storage-s3` (`git worktree add ../bloem-storage-s3-empty-root-20261015 -b test/empty-root-20261015 origin/main`, run from an existing checkout of that repository): create `empty_root_test.go`.

- [ ] **Step 1:** Read `browse_test.go` `TestDirectoryOnlyRootNestedAndEmptyLeaf` and `fake_s3_test.go` to reuse the fake backend constructor and the request helper. Write `TestMovieLayoutBrowsesUnderEveryPrefix` using the same fake with keys `movies/10s/a.mkv`, `movies/10s/b.mkv`, `movies/20s/c.mkv`, `movies/README.txt`. For prefixes `""`, `"movies/"` and `"movies/10s/"` assert directory-only `List` of the source root returns, respectively: `[movies/]`, `[10s/, 20s/]`, and **no entries** (the leaf case that produces "No subfolders" in the picker). Add `TestFilesystemGatewayMarkersStillListFolders` with the same keys plus explicit marker objects `movies/`, `movies/10s/`, `movies/20s/` and no `CommonPrefixes`, asserting `10s/` and `20s/` are still returned for prefix `movies/`.
- [ ] **Step 2:** `go test ./... -run 'MovieLayout|FilesystemGatewayMarkers' -count=1`. Expected: PASS; if the marker-only case FAILS, that is the production defect: fix `delimitedChildren` in `provider.go` so markers without `CommonPrefixes` produce directories, rerun `go test ./... -count=1`, and note the change for a patch release.
- [ ] **Step 3: Commit.** `git add empty_root_test.go [provider.go] && git commit -m "test(s3): pin movie-layout browsing for every prefix and gateway markers"`

---

### Task 12: Playwright journey

**Files:** Create `web/e2e/storage-sources.spec.ts`.

- [ ] **Step 1:** Copy the harness (profile fixtures, `InertWebSocket` init script, route stubs) from the top of `web/e2e/storage-folders.spec.ts` unchanged. Add routes: `GET **/native-storage/capabilities` returning `s3_catalog_discovery: true`; `GET **/native-storage/artifacts` returning one catalog S3 artifact with the manifest-shaped `config_schema` copied from `bloem-storage-s3/manifest.json` (`json_schema` string and `admin_form`); `POST **/native-storage/catalog/installations/discover` capturing the body and returning `201` with a source whose `connection_summary` matches the posted bucket/prefix; stateful `GET **/native-storage/sources` that returns every source created so far.
- [ ] **Step 2:** Journey at 390 and 1440 px: open Admin → Plugins, click Add source → S3, fill bucket `media`, prefix `movies/`, region `us-east-1`, endpoint `https://s3.example.com`, access/secret keys, Save; repeat for bucket `archive`, prefix empty; assert two rows with the right summaries and that both captured bodies have `provider_source_id === config.connection.source_id`, neither contains a `state_path`, and neither request URL or body appears in `console` output. Then open a Movies library form, pick source `media`, browse (stubbed `movies/` listing with `10s/`, `20s/`), select both, save, and assert one mutation with two roots; reopen and assert both persisted.
- [ ] **Step 3:** `cd web && npx playwright test e2e/storage-sources.spec.ts` → PASS.
- [ ] **Step 4: Commit.** `git add web/e2e/storage-sources.spec.ts && git commit -m "test(web): journey for adding two S3 sources and selecting folders"`

---

### Task 13: Catalog entry for S3 (separate repo, separate review)

**Files:** `Bloem-Studios/bloem-plugins` → `plugins.json`.

- [ ] **Step 1:** In a new worktree of `bloem-plugins`, add an object for `bloem.storage.s3` mirroring the Bookwarehouse entry's keys (`plugin_id`, `display_name`, `repository_url` = `https://github.com/Bloem-Studios/bloem-storage-s3`, `source_version`, `sdk_version`, `installation_mode: "native"`, `status: "requires_native_approval"`, `summary`, `requirements`, `license_spdx`, `source_visibility`, `release_url` and `checksums_url` ending `.../releases/tag/s3-v<ver>` and `.../releases/download/s3-v<ver>/checksums.txt`, and `binaries` keyed `linux/amd64` with `url = .../releases/download/s3-v<ver>/plugin-linux-amd64` and the SHA-256).
- [ ] **Step 2:** Verify before committing: the SHA-256 must equal the digest in the host's static approval map for the S3 version being published (compare with the production approvals file's `bloem.storage.s3` entry and with `sha256sum` of the release binary); validate the JSON with `python3 -m json.tool plugins.json`.
- [ ] **Step 3:** Do **not** push or tag a release. Hand the diff to Jim for approval (publishing makes the entry live for every Bloem host).

---

### Task 14: Final verification, seams, handoff

- [ ] **Step 1: Full checks.** `go build ./... && go vet ./internal/plugins ./internal/nativestorage ./internal/api/... && go test ./internal/plugins ./internal/nativestorage ./internal/api/... ./internal/apiv2/... -count=1` (DB-dependent tests that skip without a database are acceptable; record which skipped), `make verify-bloem-openapi`, `cd web && npx tsc --noEmit -p . && npx eslint src && npx vitest run && npx playwright test e2e/storage-sources.spec.ts e2e/storage-folders.spec.ts`. All must pass.
- [ ] **Step 2: Integration with a database.** Run the registry/source-management integration tests that need PostgreSQL the way `DEVELOPMENT.md` describes (separate private test database), then add and run one test in `internal/plugins/bloem_storage_connection_test.go` named `TestRotateCredentialsKeepsOtherFieldsDB` guarded the same way as `bloem_storage_registry_db_test.go`: install a fake S3 source with entries, rotate, assert credentials changed, `configuration_revision` incremented, bucket/prefix unchanged, and rotating a Bookwarehouse source returns `source_unavailable`.
- [ ] **Step 3: Seam ledger.** Read the seam section of `BLOEM_AGENTS.md`. List every shared Silo file this branch changed (`git diff --name-only origin/main | grep -v 'bloem'`), update the ledger as that document directs, and keep the count justified.
- [ ] **Step 4: Handoff.** Do not push, tag or deploy. Write the OmniMem handoff (project `bloem-server`) listing branch, commits, what was verified, and the remaining approvals: push branch, publish S3 catalog entry (Task 13), build image, deploy to CT135, then Jim enters live S3 credentials through the new form.
