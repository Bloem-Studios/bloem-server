# Vondel Full Plugin Forks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the complete private Vondel catalog/plugin repository family, import every legally redistributable pinned upstream snapshot with clean Vondel history, and leave explicit permission gates for the two unlicensed upstreams.

**Architecture:** Each target is an independent private repository with a zero-parent Vondel root, a fetch-only upstream remote, preserved upstream license/provenance, Vondel build identity, and unchanged Silo v1 wire/storage identities. All imported Go plugins pin the immutable private SDK release `github.com/Vondel-Media/vondel-plugin-sdk v0.13.3` without `replace`. Catalog publication and plugin releases remain a later phase; this plan establishes reviewed, testable source forks without making anything public.

**Tech Stack:** Go 1.26, Protocol Buffers/gRPC, HashiCorp go-plugin, GitHub private repositories, GitHub Actions, shell release guards.

## Global Constraints

- Every target repository must remain `PRIVATE`; no workflow may change visibility or publish to a public registry.
- Import source through `git archive` into a new `git init -b main` repository; do not retain upstream Git history, tags, workflows that dispatch to Silo, or reserved visual assets.
- Preserve `silo.plugin.v1`, `silo_api_version: v1`, all `silo.*` plugin IDs, capability IDs/types, URI schemes, config keys, and stored provider keys.
- Change only repository/module/documentation/presentation ownership to Vondel.
- Preserve existing license files byte-for-byte and add `NOTICE` or `UPSTREAM.md` with the exact source URL, tag/commit, modifications, and non-affiliation statement.
- Do not copy Ebook or Autoscan ARR source until the copyright holder supplies a redistribution license or written permission. Their Vondel repositories are private permission-gate repositories only in this plan.
- Do not copy tracked TMDB/TVDB provider credentials into Vondel history. Their first Vondel roots must use host-supplied secret configuration and tests must prove missing credentials fail safely.
- Use `VONDEL_MODULES_TOKEN` only at CI boundaries to read the private SDK; never commit a credential-bearing URL or print the token.
- Preserve release asset names `plugin-linux-amd64`, `plugin-linux-arm64`, `plugin-darwin-arm64`, and `checksums.txt` for later catalog compatibility.

---

### Task 1: Create and Import the Private Vondel Catalog

**Files:**
- Create repository: `/Users/jimcole/projects/vondel-plugins`
- Import: `LICENSE`, `README.md`, `go.mod`, `go.sum`, `catalog/**`, `cmd/update-catalog/**`, `manifest.json`
- Create: `NOTICE`
- Create: `.github/workflows/ci.yml`
- Modify: `.github/workflows/update-catalog.yml`
- Delete from import: `.github/workflows/update-manifest.yml`

**Interfaces:**
- Consumes: `Silo-Server/silo-plugins@934b867f69e6b140dcaf90efe56c43da63893f3f` and SDK `v0.13.3`.
- Produces: private repository `Vondel-Media/vondel-plugins`, module `github.com/Vondel-Media/vondel-plugins`, and allowlisted updater support for the six licensed Vondel plugin repositories.

- [ ] **Step 1: Write failing identity and updater-security tests**

Add tests which require the Vondel module/import path, reject a repository outside this literal allowlist, and reject duplicate/unsupported release assets:

```go
var allowedRepositories = map[string]struct{}{
    "Vondel-Media/vondel-plugin-metadb": {},
    "Vondel-Media/vondel-plugin-tmdb": {},
    "Vondel-Media/vondel-plugin-tvdb": {},
    "Vondel-Media/vondel-plugin-audiobook-metadata": {},
    "Vondel-Media/vondel-plugin-manga-metadata": {},
}
```

Run `GOWORK=off go test ./...`; expect failure because Vondel identity and allowlist validation do not exist.

- [ ] **Step 2: Import the exact snapshot into a clean root**

Archive commit `934b867f69e6b140dcaf90efe56c43da63893f3f`, initialize `main`, preserve `LICENSE`, remove the stale manual updater workflow, add upstream provenance, disable pushes to the `upstream` remote, and verify the target repository is private before pushing.

- [ ] **Step 3: Implement Vondel identity and fail-closed updating**

Set the module/import path to `github.com/Vondel-Media/vondel-plugins`, pin SDK `v0.13.3`, make the updater accept only the literal allowlist, require a published non-draft/non-prerelease release, reject duplicate platform binaries, and require every advertised platform asset plus `checksums.txt`.

- [ ] **Step 4: Make automation private-only**

Add CI for test/vet/build and change the updater workflow to use `VONDEL_CATALOG_PUSH_TOKEN` and `VONDEL_CATALOG_SOURCE_TOKEN`, run tests before mutation, and contain no Silo dispatch endpoint or visibility/publication action.

- [ ] **Step 5: Verify and commit**

Run:

```bash
GOWORK=off go mod tidy -diff
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./...
jq empty manifest.json
git diff --check
```

Commit and push `main`; confirm `gh repo view Vondel-Media/vondel-plugins --json visibility` returns `PRIVATE`.

### Task 2: Import MetaDB with Vondel Build Identity

**Files:**
- Create repository: `/Users/jimcole/projects/vondel-plugin-metadb`
- Import all 13 tracked source files from the pinned snapshot
- Create: `NOTICE`
- Create: `.github/workflows/ci.yml`
- Modify: `go.mod`, `go.sum`, `main.go`, `main_test.go`, `manifest.json`, `README.md`, `metadata/*.go`, `provider/*.go`

**Interfaces:**
- Consumes: `RXWatcher/silo-plugin-metadb@daf85c59aac440538d13223db6d644ccd240e345` and SDK `v0.13.3`.
- Produces: private module `github.com/Vondel-Media/vondel-plugin-metadb` with plugin ID `silo.metadb` and `metadata_provider.v1/metadb` unchanged.

- [ ] **Step 1: Add failing identity/manifest tests**

Require `silo.metadb`, API `v1`, the existing media priorities, no `replace`, the Vondel module path, and a complete Vondel catalog presentation block whose source URL is `https://github.com/Vondel-Media/vondel-plugin-metadb`.

- [ ] **Step 2: Clean-root import and identity rewrite**

Import the exact commit, preserve AGPL-3.0-or-later, add non-affiliation provenance, rewrite self/SDK imports, pin SDK `v0.13.3`, and keep every `SILO_METADB_*` runtime configuration key unchanged.

- [ ] **Step 3: Resolve the image-resolver discrepancy explicitly**

Keep the imported capability surface unchanged in this import plan: do not advertise `image_resolver.v1` until a separate behavior test proves the existing implementation is registered and compatible. Document that limitation in `README.md`.

- [ ] **Step 4: Add private CI and verify**

Configure private SDK resolution, test/vet/build all supported targets, guard against `replace`, public publication, Silo dispatch, and credentials, then run the full suite and push the private repository.

### Task 3: Import TMDB Without the Embedded Provider Credential

**Files:**
- Create repository: `/Users/jimcole/projects/vondel-plugin-tmdb`
- Import snapshot files except unsafe upstream workflow behavior
- Create: `NOTICE`, `scripts/verify-private-release.sh`
- Modify: `go.mod`, `go.sum`, `main.go`, `main_test.go`, `manifest.json`, `README.md`, `provider/client.go`, `provider/provider.go`, `provider/provider_test.go`, `.github/workflows/*.yml`

**Interfaces:**
- Consumes: `Silo-Server/silo-plugin-metadata-tmdb@v1.2.21` (`7bae6ba7d99f49587128dfcb0a56bee6800c6ad0`) and SDK `v0.13.3`.
- Produces: private module `github.com/Vondel-Media/vondel-plugin-tmdb`; unchanged plugin ID `silo.tmdb`, metadata provider `tmdb`, image scheme `tmdb://`, and host-supplied `api_key` global secret.

- [ ] **Step 1: Add failing credential and identity tests**

Tests must prove the tracked upstream key is absent from source and built binaries, missing/blank `api_key` returns an actionable configuration error, configured values reach the TMDB client without appearing in logs/errors, and all original IDs/priorities remain exact.

- [ ] **Step 2: Import safely and implement host configuration**

Before the zero-parent commit, remove the source literal, add password-form global config schema key `api_key`, pass it through `ConfigureRequest.GlobalConfig`, and store it only in the in-memory provider/client. Do not rename compatibility IDs or embed the key with linker flags.

- [ ] **Step 3: Rebrand build identity and automation**

Rewrite Vondel module/source/publisher URLs, pin SDK `v0.13.3`, retain TMDB attribution/non-endorsement language, use tag-only private releases, remove Silo catalog dispatch, and require exact-tag tests before later release creation.

- [ ] **Step 4: Verify and push privately**

Run tests/race/vet/build for all three platforms, the credential string scan, embedded-manifest inspection, release guard failure tests, and private visibility check.

### Task 4: Import TVDB Without the Embedded Provider Credential

**Files:**
- Create repository: `/Users/jimcole/projects/vondel-plugin-tvdb`
- Create: `NOTICE`, `scripts/verify-private-release.sh`
- Modify: `go.mod`, `go.sum`, `main.go`, `main_test.go`, `manifest.json`, `README.md`, `provider/client.go`, `provider/provider.go`, `provider/*_test.go`, `.github/workflows/*.yml`

**Interfaces:**
- Consumes: `Silo-Server/silo-plugin-metadata-tvdb@v1.2.24` (`a04e5a21427e2d9c2f63522603190c81f0017da8`) and SDK `v0.13.3`.
- Produces: private module `github.com/Vondel-Media/vondel-plugin-tvdb`; unchanged plugin ID `silo.tvdb`, provider `tvdb`, image scheme `tvdb://`, and host-supplied `api_key` global secret.

- [ ] **Step 1: Add failing credential and identity tests**

Mirror Task 3 for the tracked TVDB project key, while preserving its bearer token as an in-memory runtime value and preserving series/season/episode priorities.

- [ ] **Step 2: Import safely and implement host configuration**

Remove the source key before the first root commit, add password-form `api_key` configuration, connect it to the TVDB login/client path, redact it from errors/logs, and retain official-server protocol compatibility.

- [ ] **Step 3: Rebrand, harden automation, verify, and push**

Apply the Vondel identity/private-release rules from Task 3, preserve TVDB provider attribution, run all upstream and new tests, and confirm the target remains private.

### Task 5: Import Licensed Audiobook and Manga Plugins

**Files:**
- Create repositories: `/Users/jimcole/projects/vondel-plugin-audiobook-metadata`, `/Users/jimcole/projects/vondel-plugin-manga-metadata`
- Create in each: `NOTICE`, `scripts/verify-private-release.sh`
- Modify in each: `go.mod`, `go.sum`, `main.go`, `main_test.go`, `manifest.json`, `README.md`, `.github/workflows/*.yml`, all self-importing Go files
- Modify Manga: `provider/mangadex.go`

**Interfaces:**
- Consumes: Audiobook `v0.1.4` at `f85c630ecbbe5ef760cae1af4b8fdfb1d666397c`; Manga `v0.1.1` at `1458a58663081bff45da3230f783d55d4955852a`; SDK `v0.13.3`.
- Produces: private Vondel modules preserving `silo.audiobook-metadata` / `audiobook-metadata` and `silo.manga-metadata` / `manga-metadata`.

- [ ] **Step 1: Add failing identity/provenance tests in each repository**

Require exact compatibility IDs/config keys/priorities, exact Vondel module and presentation URLs, no `replace`, preserved AGPL license hash, and no Silo release dispatch.

- [ ] **Step 2: Import exact licensed snapshots into clean roots**

Preserve fixture attribution and provider terms, add upstream notices, rewrite only build/project identities, pin SDK `v0.13.3`, and change MangaDex's user-agent repository URL to Vondel.

- [ ] **Step 3: Add private CI/release guards and verify**

Run tests/race/vet/build, keep Manga's `MANGABAKA_LIVE=1` check optional and non-secret, verify private visibility, and push both repositories.

### Task 6: Create Permission-Gated Ebook and Autoscan ARR Repositories

**Files:**
- Create repositories: `/Users/jimcole/projects/vondel-plugin-ebook-metadata`, `/Users/jimcole/projects/vondel-plugin-autoscan-arr`
- Create in each: `README.md`, `UPSTREAM.md`, `.gitignore`

**Interfaces:**
- Consumes metadata only from Ebook `v0.1.1` (`70d8acb4dbb68f8cc594099258211cd9a3a3082f`) and ARR `v0.1.2` (`7987ddae852549f5f2ef4e00b6f25dfa5497ddad`).
- Produces: private reserved Vondel repository names with no copied upstream source and a machine-readable/documented permission gate.

- [ ] **Step 1: Create explicit legal-gate roots**

Each README must state that the upstream snapshot contains no license, no source has been copied, the repository is private, and implementation is blocked pending a license or written permission. `UPSTREAM.md` records the exact source/tag/commit for future review without reproducing upstream code.

- [ ] **Step 2: Add a fail-closed import marker**

Track `.gitignore` plus a root marker `SOURCE_IMPORT_BLOCKED` containing only the upstream URL, commit, and reason. Do not add manifests, Go source, fixtures, or workflows copied from upstream.

- [ ] **Step 3: Push and verify privacy**

Create both repositories privately, push the documentation-only roots, and confirm their Git trees contain no upstream source and GitHub visibility is `PRIVATE`.

### Task 7: Cross-Repository Fork Audit

**Files:**
- Create: `/Users/jimcole/projects/vondel-server/docs/plugin-fork-inventory.md`

**Interfaces:**
- Consumes: all eight private target repositories and their origin/upstream state.
- Produces: auditable inventory of source provenance, license status, SDK pin, compatibility IDs, privacy, CI status, and remaining release/catalog gates.

- [ ] **Step 1: Verify every repository**

For code imports run their full tests and inspect root commits, remotes, licenses, module paths, manifests, workflows, and GitHub visibility. For Ebook/ARR assert only the permission-gate files exist.

- [ ] **Step 2: Verify no secret or publication regressions**

Search current files and reachable Vondel history for the known upstream TMDB/TVDB credentials, credential-bearing URLs, `replace`, Silo dispatch tokens/endpoints, public-registry publication, and visibility mutation. Expected: no findings except factual upstream URLs in notices.

- [ ] **Step 3: Record the inventory and commit**

Document exact Vondel HEADs, upstream pins, license identifiers, tests, privacy evidence, and the fact that private GitHub catalog/releases cannot yet be consumed anonymously by the current server. State that the next phase is private releases plus a local/authenticated staging delivery path and the core movie/series scan acceptance suite.

## Execution Order

Execute Tasks 1–6 with a fresh implementer and reviewer per task. Task 7 is the final whole-family gate. Do not wait for Ebook/ARR permission to complete the six lawful source forks; their explicit gated repositories satisfy this plan without copying unlicensed code.
