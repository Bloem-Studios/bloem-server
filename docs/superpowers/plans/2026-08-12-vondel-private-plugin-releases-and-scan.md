# Vondel Private Plugin Releases and Scan Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import the two owner-authorized plugins, privately release all seven Vondel plugins, materialize a token-free staging catalog, and prove real installation plus deterministic media scanning without changing public defaults.

**Architecture:** Ebook and Autoscan ARR are imported from pinned upstream snapshots into their existing private gate repositories and licensed AGPL-3.0-or-later. All seven plugins receive immutable private patch releases with three binaries plus checksums. A trusted materializer converts authenticated private GitHub Releases into a network-local static catalog containing relative, token-free URLs; Vondel Server installs explicitly from that external repository and runs real plugin/runtime/scanner persistence paths against deterministic fixture APIs.

**Tech Stack:** Go 1.26, GitHub Actions/private Releases, SHA-256, PostgreSQL, `httptest`, gRPC/go-plugin, Vondel SDK `v0.13.3`.

## Global Constraints

- All repositories, releases, assets, and catalog data remain private. Ebook and Autoscan ARR remain permanently private.
- Never change `internal/plugins.DefaultRepositoryURL`, official catalog enablement, or public server defaults.
- Never embed GitHub/provider credentials in source, history, binaries, URLs, catalog JSON, fixtures, logs, or committed configuration.
- Preserve `silo.plugin.v1`, `silo_api_version: v1`, all `silo.*` plugin IDs, capability IDs/types, priorities, schemes, and config keys.
- Every plugin pins `github.com/Vondel-Media/vondel-plugin-sdk v0.13.3` without `replace`.
- Release tags are annotated stable SemVer, resolve to reviewed `origin/main`, and are never moved/reused after push.
- Release assets are exactly `plugin-linux-amd64`, `plugin-linux-arm64`, `plugin-darwin-arm64`, and `checksums.txt`; checksum entries use bare filenames.
- Private SDK credentials execute only in a trusted checkout-free prefetch job. Repository-controlled code runs in separate secretless jobs.
- Acceptance endpoint overrides are unexported build-time variables only; ordinary release binaries retain official provider endpoints.

---

### Task 1: Import and Rebrand Ebook Metadata

**Files:**
- Modify repository: `/Users/jimcole/projects/vondel-plugin-ebook-metadata`
- Import pinned snapshot source beneath existing gate root
- Create: `LICENSE`, `NOTICE`, `scripts/verify-private-source.sh`, private CI/release workflows
- Modify: `go.mod`, `go.sum`, `manifest.json`, `README.md`, all self-importing Go files/tests

**Interfaces:**
- Consumes: `Silo-Server/silo-plugin-metadata-ebook@v0.1.1` commit `70d8acb4dbb68f8cc594099258211cd9a3a3082f`, owner authorization spec, SDK `v0.13.3`.
- Produces: private AGPL-3.0-or-later module `github.com/Vondel-Media/vondel-plugin-ebook-metadata`, candidate version `0.1.2`.

- [ ] Write failing policy tests requiring the exact upstream pin, AGPL-3.0-or-later, Vondel module/presentation, `silo.ebook-metadata`, `metadata_provider.v1/ebook-metadata`, priority `ebook:2`, preserved config keys, SDK `v0.13.3`, and no `replace`/public action.
- [ ] Archive the pinned snapshot into a temporary directory and copy source files into the existing repository without importing upstream Git history; replace the four gate files with complete Vondel provenance/license documentation while preserving the zero-parent root.
- [ ] Rewrite only module/self/SDK and Vondel presentation/documentation identities; fix the stale manifest-version assertion by deriving the expected embedded manifest version.
- [ ] Add trusted `pull_request_target` CI and tag-only private release automation with strict SemVer, exact tag/manifest/HEAD/origin-main/private checks, bare checksums, exactly four assets, and no Silo dispatch.
- [ ] Run tidy-diff, tests, race, vet, three-platform builds, workflow/guard tests, secret/publication scans, `git diff --check`; commit/push and require exact-HEAD CI green.

### Task 2: Import and Rebrand Autoscan ARR

**Files:**
- Modify repository: `/Users/jimcole/projects/vondel-plugin-autoscan-arr`
- Import pinned snapshot source beneath existing gate root
- Create: `LICENSE`, `NOTICE`, `scripts/verify-private-source.sh`, private CI/release workflows
- Modify: `go.mod`, `go.sum`, `manifest.json`, `README.md`, `main*.go`, `internal/arr/**`, tests

**Interfaces:**
- Consumes: `Silo-Server/silo-plugin-autoscan-arr@v0.1.2` commit `7987ddae852549f5f2ef4e00b6f25dfa5497ddad`, owner authorization spec, SDK `v0.13.3`.
- Produces: private AGPL-3.0-or-later module `github.com/Vondel-Media/vondel-plugin-autoscan-arr`, candidate version `0.1.3`.

- [ ] Write failing policy tests requiring exact pin/license/module/presentation, `silo.autoscan.arr`, `scan_source.v1/arr`, API `v1`, preserved connection semantics, SDK `v0.13.3`, no `replace`, and no public/Silo action.
- [ ] Import the exact archive into the existing gate repository, add complete AGPL/provenance/non-affiliation files, and retain the zero-parent Vondel gate root.
- [ ] Rewrite Vondel build identity only; preserve ARR URL/API-key request fields and ensure errors/logs never contain credentials. Correct stale documentation about SDK pseudo-versions.
- [ ] Add the trusted CI/private release contract from Task 1 and keep the integration-tag real process test.
- [ ] Run normal and integration tests, race, vet, three-platform builds, guards, secret scans, `git diff --check`; commit/push and require exact-HEAD CI green.

### Task 3: Harden and Version the Existing Five Release Candidates

**Files:**
- Modify each of MetaDB, TMDB, TVDB, Audiobook, Manga: `manifest.json`, release workflow, release guard/tests
- Create in MetaDB: `.github/workflows/release.yml`, `scripts/verify-private-release.sh`, tests

**Interfaces:**
- Consumes reviewed fork HEADs and SDK `v0.13.3`.
- Produces versions MetaDB `1.0.1`, TMDB `1.2.22`, TVDB `1.2.25`, Audiobook `0.1.5`, Manga `0.1.2`.

- [ ] Add failing tests for branch/non-SemVer/tag mismatch/tag!=HEAD/not-on-origin-main/non-private/wrong-repository/missing-or-extra-assets/directory checksum/public dispatch cases.
- [ ] Patch-bump only manifest versions; preserve all compatibility and upstream provenance identifiers.
- [ ] Standardize tag-only private release workflows: full-history SHA checkout, exact annotated tag, stable SemVer, `HEAD` on `origin/main`, test/race/vet, three target builds, bare checksum verification, exactly four files, final privacy recheck, final-job-only `contents:write`.
- [ ] Reuse sanitized fixed-SDK prefetch; ensure no private module secret reaches checkout/repository code.
- [ ] Run full local gates per repository, commit/push each candidate, and require exact-HEAD CI green.

### Task 4: Tag and Verify Seven Private Releases

**Files:**
- Update ignored SDD evidence; update server inventory only after verification.

**Interfaces:**
- Consumes seven green release candidates.
- Produces private Releases: Ebook `v0.1.2`, ARR `v0.1.3`, MetaDB `v1.0.1`, TMDB `v1.2.22`, TVDB `v1.2.25`, Audiobook `v0.1.5`, Manga `v0.1.2`.

- [ ] For each repo verify clean `main==origin/main`, PRIVATE, target tag/release absent, candidate CI green, manifest/tag exact, no secret/publication regression.
- [ ] Create/push annotated tags in order MetaDB, TMDB, TVDB, Ebook, Audiobook, Manga, Autoscan; never rewrite a pushed tag.
- [ ] Require exact-SHA Release workflow success, private published non-draft/non-prerelease Release, exactly four assets, valid bare-name checksums, correct binary self-manifest, and absent credential fingerprints.
- [ ] Update `docs/plugin-fork-inventory.md` with tags, peeled SHAs, run IDs, private URLs, asset hashes, and AGPL authorization status; commit/push server.

### Task 5: Populate the Canonical Private Catalog

**Files:**
- Modify: `/Users/jimcole/projects/vondel-plugins/manifest.json`, `catalog/catalog.go`, `catalog/catalog_test.go`, `cmd/update-catalog/main.go`, updater workflow

**Interfaces:**
- Consumes seven verified Releases.
- Produces seven unique Vondel-controlled catalog entries.

- [ ] Add failing tests requiring exact seven-repository allowlist and asset-content validation: download checksums, reject malformed/duplicate/missing hashes, fetch every binary, verify SHA-256, validate supported platforms and manifest/release ID/version/source consistency.
- [ ] Expand the allowlist to all seven authorized code repositories; keep no entry for any unrelated repo.
- [ ] Authenticate only allowlisted GitHub API asset requests with step-scoped source credentials and validate all bytes before catalog mutation.
- [ ] Generate entries from exact tags; assert seven unique plugin IDs, Vondel source/publisher URLs, expected platforms, no upstream binary URL, and deterministic byte-identical second update.
- [ ] Run test/race/vet/build/JSON/secret/privacy gates; commit/push canonical private catalog.

### Task 6: Build the Token-Free Staging Materializer

**Files:**
- Create in `vondel-plugins`: `cmd/materialize-staging/main.go`, `internal/staging/materialize.go`, tests, `docs/private-staging.md`

**Interfaces:**
- Consumes private catalog plus a process-boundary read token.
- Produces `catalog.json` and filename-preserving plugin/version asset directories with only relative URLs and no credential.

- [ ] Write `httptest` failures for missing auth, non-allowlisted repos/assets, wrong hashes, path traversal, symlinks, token leakage, credential forwarding on cross-origin redirects, partial output, and nondeterminism.
- [ ] Download exact Release assets through GitHub API, authenticate only initial allowlisted API origins/paths, strip auth on redirects, verify checksum/manifests, and atomically publish executable binaries plus relative catalog URLs.
- [ ] Document one-shot materialization and network-local static serving; explicitly forbid credentials in server repository URLs or static output.
- [ ] Verify deterministic output twice, anonymous static fetch of catalog/checksums/binaries, race/vet/build, token-canary absence; commit/push.

### Task 7: Add Deterministic Provider/Autoscan Acceptance Seams

**Files:**
- Modify/test TMDB and TVDB `provider/client.go`
- Add server fixture JSON under `internal/acceptance/testdata/{tmdb,tvdb,ebook,arr}`

**Interfaces:**
- Produces acceptance-only builds using unexported linker-set endpoint variables; normal releases retain official endpoints.

- [ ] Add failing tests proving no runtime/env/global-config endpoint override and official default in ordinary builds.
- [ ] Change only unexported provider endpoint constants to linker-set variables with identical official defaults.
- [ ] Add invented deterministic TMDB movie, TVDB series/season/episode/image, Ebook metadata, and ARR history fixtures with no provider credentials.
- [ ] Verify full plugin suites and ordinary binary endpoint invariants; commit/push source changes without moving release tags.

### Task 8: Add Real Install, Runtime, and Scan Acceptance

**Files:**
- Create server: `internal/acceptance/database_test.go`, `staging_server_test.go`, `plugin_install_test.go`, `media_scan_test.go`, `autoscan_test.go`

**Interfaces:**
- Consumes materialized catalog, acceptance-built binaries, disposable PostgreSQL database.
- Produces real checksum/install/manifest/runtime/scan/rescan/timeout/autoscan evidence.

- [ ] Create a safe unique database from `VONDEL_ACCEPTANCE_ADMIN_DATABASE_URL`, run all migrations, and drop only the validated generated database on cleanup.
- [ ] Tamper a staged binary while retaining its valid checksum; explicit external `repository_id` installation must fail with no row/files.
- [ ] Install valid TMDB/TVDB/Ebook/ARR binaries through real `CatalogService`, `Installer`, stores, host, and service; assert persisted checksum/archive/manifest/capabilities, configure/start/invoke, and secret redaction.
- [ ] Scan representative movie and episodic MP4 layouts through real scanner/matcher/executor; assert matched items, IDs, artwork, season, episode, library links, and provider fixture calls.
- [ ] Rescan and require stable identities/counts/no duplicates. Force provider timeout, require unchanged stored metadata, restore health, and require recovery.
- [ ] Poll ARR fixture through `scan_source.v1`; assert changed paths/dedup semantics and no URL/API-key in logs/errors.
- [ ] Run `GOWORK=off go test -tags=integration ./internal/acceptance -count=1 -v`, record reproducible hashes/results, commit/push server.

### Task 9: Verify Official Silo Compatibility and Finalize

**Files:**
- Create server: `scripts/verify-official-silo-plugin-compat.sh` and test
- Modify: `docs/plugin-fork-inventory.md`

**Interfaces:**
- Consumes Vondel SDK `hello-scheduled-task` and official Silo baseline `1dcdd4b27ab5fcd697a32fc20f20c2400ca24688`.
- Produces manifest/start/configure/scheduled-task RPC/stop proof plus final private ecosystem inventory.

- [ ] Write failing portability/cleanup tests: exact baseline, temporary checkout, no workspace hardcode, no official mutation/push, cleanup on success/failure.
- [ ] Build the Vondel SDK `hello-scheduled-task`, inject only an external build-tagged test into a temporary detached official checkout, and assert manifest/start/configure/Run/stop.
- [ ] Verify all seven private Releases, catalog/materialization, tamper rejection, runtime/scan/rescan/timeout/autoscan, official compatibility, privacy, unchanged public defaults, and secret-free current/reachable histories.
- [ ] Update inventory with exact evidence and state all seven plugins—including permanently private Ebook/ARR—remain outside public publication; commit/push.
