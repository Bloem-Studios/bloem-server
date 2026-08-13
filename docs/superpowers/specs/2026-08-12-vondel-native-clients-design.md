# Vondel Native Clients Design

**Date:** 2026-08-12  
**Repositories:** `Vondel-Media/vondel-apple`, `Vondel-Media/vondel-android`  
**Visibility:** Private, with no planned public transition

## Goal

Create complete, independent Vondel-native Apple and Android client repositories from exact upstream Silo snapshots. The clients keep full feature and protocol compatibility with Vondel Server and official Silo servers while using new Vondel product, package, signing, and future store identities.

The result is not a GitHub fork network and does not preserve upstream Git history or release tags. Each Vondel repository starts with a clean root commit containing the complete reviewed source tree plus the required rebrand, attribution, security, and private-CI changes.

## Source snapshots

The imports are pinned to:

- `Silo-Server/silo-apple` commit `1de0bfc5f4e057a5af5bdecb132874d930669ef1`
- `Silo-Server/silo-android` commit `3efdbd90abee8dcec2fb06ea30c88fed9bb27125`

The Apple snapshot contains the iOS, tvOS, and early macOS targets. The Android snapshot contains Android phone and Android TV clients with shared Kotlin Multiplatform code. All tracked application source, tests, build logic, documentation, vendored license material, platform targets, and fixtures are imported unless a file is a Silo-owned visual trademark asset, signing/publication material, or obsolete upstream-only automation.

## Repository topology and provenance

The private target repositories are:

- `Vondel-Media/vondel-apple`
- `Vondel-Media/vondel-android`

Each repository is initialized with a clean Vondel root rather than created through GitHub's fork mechanism. A `NOTICE` file records the upstream URL, exact source commit, AGPL basis, material Vondel changes, and independent/non-endorsed status. The upstream `LICENSE` remains byte-identical, and all third-party notices and vendored licenses remain intact.

Each local repository has:

- `origin` pointing to the private Vondel repository;
- `upstream` pointing to the Silo source for fetch only;
- a disabled upstream push URL;
- no imported upstream branches, tags, releases, or reachable Git history.

## Product and build identity

All user-facing product identity becomes **Vondel**. Silo logos, wordmarks, store badges/links, official signing identities, and Silo-controlled release destinations are removed or replaced before the first Vondel commit.

New identifiers use the `media.vondel` reverse-DNS root:

### Apple

- Primary application base: `media.vondel.app`
- tvOS, notification, downloads activity, Top Shelf, and other extension identifiers use deterministic target suffixes under that base.
- Keychain access groups, app groups, associated domains, URL schemes, and signing configuration use Vondel-owned identifiers.
- Project, scheme, target, executable/archive names, display names, generated project configuration, Fastlane metadata, tests, and documentation use Vondel naming.
- No upstream Apple team ID, provisioning profile, App Store Connect key, TestFlight URL, or match repository is retained.

### Android

- Shared phone/TV Play application ID: `media.vondel.app`
- Kotlin/Java package root: `media.vondel`
- Module namespaces remain distinct beneath the Vondel root so generated resources do not collide.
- Phone and TV retain the upstream paired-listing feature-filter model and non-colliding version-code strategy.
- Gradle project name, manifests, resources, services, providers, deep links, ProGuard rules, tests, Fastlane metadata, and documentation use Vondel naming.
- No upstream Play service account, signing keystore identity, store listing URL, or release destination is retained.

These are new application identities. They are not in-place upgrades of official Silo apps; users install and authenticate them separately.

## Compatibility boundary

The rebrand must not change the server protocol. Existing `/api/v1/*` REST and WebSocket contracts, media/download semantics, authentication flows, cast/discovery frames, persisted API field names, and compatibility identifiers remain unchanged where they are wire or storage contracts.

Referential language such as “compatible with Silo servers” is permitted in compatibility documentation and connection UI. It must not present Vondel as official Silo software or use Silo as the product identity.

Internal Silo-named source symbols are renamed when they are application identity rather than protocol contracts. A compatibility allowlist documents each intentionally preserved Silo string and why it is required. Generic blanket exemptions are not allowed.

## Visual assets

Silo-owned logos, wordmarks, icons, and store imagery are excluded from the Vondel roots. Existing non-brand UI assets may be retained only when their license/notice permits it and they do not create confusing similarity.

The initial import may use a clearly Vondel-labeled neutral development icon if a final Vondel icon set is not yet available. A development placeholder cannot be submitted to a store. Final production icon/splash/store art is a separate brand-art deliverable and must pass the same asset provenance guard before release.

## Automation and privacy

Both repositories remain private. The initial automation provides private validation only:

- trusted checkout with persisted GitHub credentials disabled;
- dependency/build caches that do not expose credentials to untrusted code;
- Apple project generation, compile, and available unit tests without signing;
- Android unit tests, lint/static checks, and debug builds without signing;
- source and workflow guards for public visibility changes, releases, registries, store submission, signing material, and credential-bearing URLs.

Upstream release, TestFlight, App Store Connect, Google Play, public GitHub Release, and sideload publication workflows are removed or disabled. No tag or Vondel release is created during import. Store automation may be designed later after Vondel-owned developer accounts, signing identities, and private release policy exist.

## Import verification

Each import produces machine-checkable evidence for:

1. exact upstream commit and complete tracked-tree inventory;
2. byte-identical AGPL and preserved third-party notices;
3. a documented diff classifying every excluded or modified upstream file;
4. Vondel repository/package/bundle/application identity;
5. absence of Silo visual-brand files and unjustified user-facing Silo branding;
6. absence of embedded credentials, signing material, upstream team/store IDs, and public publication paths;
7. compatibility allowlist coverage for preserved wire/storage strings;
8. clean local repositories with `HEAD == origin/main`, private GitHub visibility, disabled upstream push, and no tags/releases;
9. successful platform-appropriate tests/builds and exact-head private CI.

Apple build checks are performed on the available macOS/Xcode environment and record any target that requires unavailable SDK/signing infrastructure. Android checks use the repository-pinned Gradle wrapper and supported JDK/Android SDK.

## Delivery order

Apple and Android imports are independent and may be prepared in parallel, but each is reviewed and verified separately. The work order is:

1. archive and inventory both exact upstream snapshots;
2. create private clean-root targets only after visibility verification;
3. apply legal/provenance and identity changes before the first push;
4. add privacy/security regression guards;
5. run local build/test gates;
6. commit and push each clean root;
7. require exact-head private CI success;
8. record both client repos in the Vondel inventory without claiming store readiness.

## Acceptance criteria

The native-client import is complete when both private Vondel repositories contain the full retained client source at the pinned snapshots, have independent Vondel build/store identities, preserve required official-server compatibility, retain all license obligations, contain no Silo visual identity or publication credentials, pass available platform tests/builds and privacy guards, and are synchronized to private `origin/main` with no tags or releases.

App Store, TestFlight, Google Play, F-Droid, and public binary distribution are explicitly outside this import phase.
