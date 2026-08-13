# Vondel Native Clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import the complete Apple and Android clients into independent, private Vondel repositories with new application identities and compatibility with both Vondel and official Silo servers.

**Architecture:** Each client is archived from an exact upstream commit into a clean-root Vondel repository. Legal/provenance, product/package identity, brand assets, automation, and regression guards are corrected before the first push; wire/storage contracts are retained through an explicit compatibility allowlist.

**Tech Stack:** Swift/SwiftUI/XcodeGen/Fastlane for Apple; Kotlin Multiplatform/Compose/Gradle for Android; GitHub Actions; AGPL-3.0-or-later.

## Global Constraints

- Repositories are `Vondel-Media/vondel-apple` and `Vondel-Media/vondel-android`, PRIVATE with no tags/releases.
- Pin Apple `1de0bfc5f4e057a5af5bdecb132874d930669ef1`; Android `3efdbd90abee8dcec2fb06ea30c88fed9bb27125`.
- Preserve complete retained source, tests, targets, build logic, AGPL and third-party notices; exclude Silo visual marks and upstream signing/publication material.
- Use new application identity root `media.vondel.app` and source namespace root `media.vondel`.
- Preserve `/api/v1/*`, WebSocket, cast/discovery, serialized, and persisted compatibility contracts.
- No upstream history/tags, credentials, signing identities, store submission, public release, or visibility mutation.

---

### Task 1: Full Apple Client Import

**Files:**
- Repository: `/Users/jimcole/projects/vondel-apple`
- Import: complete tracked archive from pinned Apple commit
- Create: `NOTICE`, compatibility/brand/privacy guard tests, private CI
- Modify: XcodeGen/project, plist, entitlements, Swift identity symbols/copy, Fastlane/docs/workflows/assets

**Interfaces:**
- Consumes: exact upstream archive and complete tree manifest.
- Produces: private Vondel iOS/tvOS/macOS source root with independent IDs and official-server compatibility.

- [ ] Add executable policy tests that fail until the exact pin, byte-identical AGPL, complete-tree accounting, `media.vondel.app` IDs, Vondel display/build identity, compatibility allowlist, private-only workflows, and absence of Silo brand/store/signing credentials hold.
- [ ] Verify the RED tests fail for the expected upstream identities, then import the exact archive into a new clean-root repository without upstream Git history.
- [ ] Add `NOTICE`, retain third-party notices/licenses, replace Silo-owned visual assets with neutral Vondel development assets, and classify every excluded/modified upstream file.
- [ ] Rewrite project/scheme/target/archive/display identity, bundle/extension/keychain/app-group/URL identifiers and application-only symbols to Vondel; retain allowlisted protocol/storage strings.
- [ ] Remove upstream publication/signing workflows and secrets; add credentialless private CI for project generation, available unit tests, and unsigned simulator/platform builds.
- [ ] Run guard tests, project generation, Swift tests/builds available on installed Xcode, secret/signing/store scans, tree/legal diff, and `git diff --check`.
- [ ] Create `Vondel-Media/vondel-apple` PRIVATE, verify visibility before first push, configure fetch-only upstream, commit one clean root, push, and require exact-head CI green.

### Task 2: Full Android Client Import

**Files:**
- Repository: `/Users/jimcole/projects/vondel-android`
- Import: complete tracked archive from pinned Android commit
- Create: `NOTICE`, compatibility/brand/privacy guard tests, private CI
- Modify: Gradle settings/modules, manifests/resources, Kotlin/Java packages, Fastlane/docs/workflows/assets

**Interfaces:**
- Consumes: exact upstream archive and complete tree manifest.
- Produces: private Vondel phone/Android TV source root with shared independent application ID and official-server compatibility.

- [ ] Add executable policy tests that fail until the exact pin, byte-identical AGPL, complete-tree accounting, `media.vondel.app` application ID, `media.vondel` namespaces, Vondel user/build identity, compatibility allowlist, private-only workflows, and absence of Silo brand/store/signing credentials hold.
- [ ] Verify the RED tests fail for expected upstream identities, then import the exact archive into a new clean-root repository without upstream Git history.
- [ ] Add `NOTICE`, retain third-party notices/licenses, replace Silo-owned visual assets with neutral Vondel development assets, and classify every excluded/modified upstream file.
- [ ] Mechanically relocate Kotlin/Java packages and rewrite imports/namespaces/manifests/services/providers/deep links to Vondel; retain allowlisted wire/storage strings and paired phone/TV version-code behavior.
- [ ] Remove upstream publication/signing workflows and secrets; add private CI for wrapper verification, unit tests, lint/static checks, and unsigned phone/TV debug builds.
- [ ] Run policy tests, Gradle tests/lint/builds with the repository wrapper, credential/signing/store scans, tree/legal diff, and `git diff --check`.
- [ ] Create `Vondel-Media/vondel-android` PRIVATE, verify visibility before first push, configure fetch-only upstream, commit one clean root, push, and require exact-head CI green.

### Task 3: Cross-Client Verification and Inventory

**Files:**
- Modify: `/Users/jimcole/projects/vondel-server/docs/client-fork-inventory.md`

**Interfaces:**
- Consumes: exact clean roots and CI evidence from Tasks 1–2.
- Produces: auditable two-client handoff without claiming store readiness.

- [ ] Verify both repos PRIVATE, clean, `HEAD == origin/main`, one zero-parent root, upstream push disabled, no tags/releases, exact source pins/licenses, no credential/store publication or unallowlisted Silo branding.
- [ ] Run protocol/API endpoint compatibility checks against Vondel Server contracts and document intentionally retained Silo wire/storage identifiers.
- [ ] Record exact roots, pins, CI runs, build/test results, identity roots, attribution, and explicit not-store-ready status in the inventory.
- [ ] Commit/push the server inventory and run docs/link/diff gates relevant to the new file.
