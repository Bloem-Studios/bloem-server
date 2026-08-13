# Vondel Clean-Room Native Clients Design

**Date:** 2026-08-12  
**Status:** Approved design  
**Products:** Vondel for Apple platforms and Vondel for Android platforms

## Objective

Build two genuinely independent, publishable native client families for Vondel and compatible official Silo servers. The clients cover every supported media category from their first store release:

- movies;
- episodic television;
- music;
- audiobooks;
- ebooks;
- manga.

They also include the complete capability-gated Live TV experience defined by `2026-08-12-vondel-prairie-livetv-design.md`: channel guide, live playback, timeshift, one-off/series DVR, conflicts and recordings.

All target platforms exist from the beginning. tvOS and Android TV lead product development, but store submission waits until the complete Apple and Android platform matrices satisfy the acceptance criteria.

These applications are not rewrites, reskins, ports, or disguised forks of the Silo reference clients. They begin in empty repositories and use original source code, architecture, navigation, visual design, assets, naming, tests, and release metadata. Compatibility is implemented from documented server interfaces and black-box protocol fixtures.

## Policy and review posture

The goal is not to obscure provenance or mislead reviewers. The applications must be independently engineered and honestly described as compatible clients for user-owned media servers.

Apple Guideline 4.3 rejects apps that are indistinguishable from widely available apps, while Guideline 5.2 requires developers to own or license included intellectual property and avoid copycat representations. Google Play's repetitive-content policy rejects apps that copy another app's experience without original content or value.

Vondel addresses those requirements through original product value, verifiable clean-room development, accurate metadata, complete functionality, and a distinct media-mode experience. The review package will state what the product does and provide a stable demo environment; it will not claim that cosmetic changes alone make the app independent.

Official policy references:

- [Apple App Review Guidelines](https://developer.apple.com/app-store/review/guidelines/)
- [Google Play repetitive-content policy](https://support.google.com/googleplay/android-developer/answer/9899034)

## Repository boundary

Three new repositories start empty with zero-parent Vondel root commits:

- `Vondel-Media/vondel-client-contracts`
- `Vondel-Media/vondel-apple`
- `Vondel-Media/vondel-android`

The private reference repositories remain separate and read-only:

- `Vondel-Media/silo-apple-reference`
- `Vondel-Media/silo-android-reference`

The reference repositories are never Git remotes, dependencies, submodules, build inputs, source generators, or copy sources for the new applications. They may be consulted only to compile a high-level feature coverage list and observe public black-box behavior. Reference access is documented in the provenance ledger.

## Clean-room process

### Contract team boundary

The contracts repository is derived from Vondel Server route definitions, checked-in API documentation, invented fixtures, and observed network contracts against controlled servers. It defines:

- endpoint paths and HTTP methods;
- request and response schemas;
- WebSocket event schemas;
- capability identifiers and version negotiation;
- authentication and profile scoping rules;
- playback-plan and offline-bundle schemas;
- typed error categories;
- deterministic invented fixtures;
- platform-neutral conformance cases.

It contains no client UI, playback implementation, copied client models, or reference-client internal names.

### Application team boundary

Apple and Android implementations consume the contracts repository as documentation and test fixtures. They do not read or incorporate reference source during implementation. Each feature report records:

- the server specification or route that authorized the behavior;
- the original product/design decision;
- new source files and tests;
- dependencies and licenses;
- any unavoidable standardized protocol term.

An originality guard rejects copied reference assets, distinctive filenames, internal symbols, package layouts, test names, and source fragments. Common platform/framework words and required server protocol terms are explicitly allowlisted.

## Platform architecture

### Apple

The Apple repository uses Swift, SwiftUI, structured concurrency and native media frameworks.

Targets:

- iOS;
- iPadOS adaptive layout;
- tvOS;
- macOS.

Core components:

- typed HTTP/WebSocket gateway generated or hand-written from Vondel contracts;
- Keychain-backed account/profile vault;
- server/account/profile-scoped local persistence;
- independent Watch, Listen and Read repositories;
- AVFoundation/Core Media playback engines selected through an original route planner;
- background download and offline-bundle verifier;
- ebook and manga rendering components;
- tvOS-native remote focus/navigation system;
- SwiftUI adaptive navigation for phone, tablet and macOS.

### Android

The Android repository uses Kotlin, coroutines/Flow, Jetpack Compose and Media3. Phone/tablet and television are distinct application surfaces over shared domain and infrastructure modules.

Targets:

- Android phone;
- Android tablet adaptive layout;
- Android TV.

Core components:

- typed HTTP/WebSocket gateway from Vondel contracts;
- Android Keystore-backed account/profile vault;
- server/account/profile-scoped Room persistence;
- independent Watch, Listen and Read repositories;
- Media3 playback engines selected through an original route planner;
- WorkManager-backed synchronization and downloads;
- ebook and manga rendering components;
- Android TV-native focus/navigation system;
- Compose adaptive navigation for touch devices.

### No shared UI or application code

Apple and Android share semantics through the contracts and conformance fixtures, not a cross-platform runtime or UI framework. Equivalent behaviors are implemented independently with platform-native conventions. This avoids a template-like product, protects player performance and produces experiences appropriate to each operating system.

## Product information architecture

Vondel is organized by what the user intends to do rather than by server database types.

### Watch

Movies, series, seasons and episodes; continue watching; watchlists; collections; trailers where provided; playback source selection; subtitles; audio tracks; chapters; quality controls; progress and next-up behavior.

### Listen

Artists, albums, tracks, playlists, audiobook titles and chapters; queue management; background audio; lock-screen/system media controls; progress; speed and sleep controls for audiobooks; offline listening.

### Read

Ebooks and manga; title/series browsing; chapters/issues; pagination; typography and themes; left-to-right/right-to-left manga direction; progress and bookmarks; verified offline reading.

### Library

Unified filtering, saved collections, downloads, server sources and profile-aware organization across all media types.

### Search

One search flow groups results by Watch, Listen and Read, with filters that reveal media-specific fields without forcing users into separate search screens.

### Now

An original cross-media activity surface combines continue watching, continue listening, continue reading, active downloads, recently added items and device handoff. This is the central differentiator and is not modeled on the reference clients.

### Live TV

An original TV-first spatial guide presents channels, now/next, program details, live playback and live-edge/timeshift state. DVR flows cover record/cancel, series rules, deterministic conflicts and completed/failed recordings. Live TV appears only when the connected server advertises the versioned capability; compatible official Silo servers without it show no dead navigation.

## TV-first interaction model

tvOS and Android TV are the first production-complete vertical slices. They use an original cinematic spatial composition with large editorial canvases, deliberate remote focus movement and platform-native transport controls.

TV supports all media categories:

- video playback for Watch;
- full music and audiobook navigation/playback for Listen;
- remote-friendly ebook and manga page/panel viewing for Read;
- optional handoff of the current item and position to a signed-in mobile device.

The TV applications are not enlarged phone layouts. Focus order, remote gestures, information density, reader controls and playback overlays are independently designed for ten-foot use.

## Connection, identity and capability flow

Connection flow:

1. discover a local server or accept a manually entered HTTPS URL;
2. validate the origin and probe server identity/capabilities;
3. authenticate through the documented Vondel/Silo endpoint;
4. store tokens in the platform security vault;
5. select account/profile and create a strict scope identifier;
6. open scoped persistence and synchronize supported content.

Every cached entity and pending mutation is keyed by server, account and profile. Switching any identity closes the old scope before rendering the new one. Tokens, URLs and user media metadata are never logged by default.

Both Vondel and compatible official Silo servers are supported. Capability negotiation determines available functionality. Missing optional capabilities produce a visible unavailable state or a documented fallback; the client never silently assumes a Vondel-only extension exists.

## Playback and reader design

The server remains authoritative for playback policy, access restrictions and transcoding plans. Each client validates a playback plan, chooses an appropriate native route and reports progress through documented endpoints.

Separate session coordinators exist for:

- video;
- music;
- audiobooks;
- ebooks;
- manga.

They share only small value types for content identity, position, queue membership and retry policy. Video route selection, music queue behavior, audiobook chapter/speed state and reader pagination remain independent so one media mode cannot destabilize another.

Downloads use the server's offline-bundle contract. A bundle is written to staging, hash/manifest validated, then atomically promoted. Partial or invalid bundles never appear as available. Revocation, expiration and account/profile changes remove or quarantine content according to the documented policy.

## Synchronization and recovery

A durable platform-native work queue handles:

- playback and reading progress;
- favorites and watchlists;
- playlists and queue mutations;
- bookmarks;
- download lifecycle;
- device registration and notification state where enabled.

Operations use idempotency keys when supported and are scoped to the exact server/account/profile. Retries use bounded exponential backoff with network-awareness. Authentication refresh is single-flight. A profile/server switch cancels foreground work and prevents stale results from entering the new scope.

Errors are classified as connection, TLS/origin, authentication, authorization, capability, policy, malformed response, media preparation, playback, reader, download, storage or server failure. User messages state a safe recovery action. Diagnostics are local-first, redact credentials and require explicit consent before upload.

## Original visual identity

Vondel receives an original visual system and store package:

- Vondel-owned icon, wordmark, splash and store imagery;
- original typography, color, motion and spacing tokens;
- original poster/artwork treatments and focus states;
- original navigation structure and screen composition;
- original empty, loading, error and offline states;
- original screenshots and preview videos using invented demo content.

No Silo artwork, screenshots, copy, source asset, component layout or metadata is reused. Referential wording such as “compatible with Silo servers” may appear where accurate but is not used as product identity or keyword stuffing.

## Testing strategy

### Contract conformance

The contracts repository runs deterministic cases against invented fixtures and a disposable Vondel Server. Each endpoint covers success, authentication/authorization failure, missing capability, malformed response, timeout and cancellation.

### Platform tests

Both clients require:

- unit tests for parsing, identity scoping, state transitions and recovery;
- integration tests against the disposable server;
- UI navigation/focus tests for every target;
- playback matrices for direct play, remux and transcode;
- subtitle, audio-track, chapter, HDR and network-transition coverage where applicable;
- Watch, Listen and Read progress/offline/recovery tests;
- secure-storage and token-redaction tests;
- accessibility and localization gates;
- background execution and lifecycle tests;
- privacy manifest/data-safety verification.

TV focus tests are first-class suites and are never derived mechanically from phone UI tests.

### Full launch matrix

Store submission is blocked until movies, episodic television, music, audiobooks, ebooks and manga are functional on:

- iPhone;
- iPad;
- macOS;
- Apple TV;
- Android phone;
- Android tablet;
- Android TV.

The same seven-target matrix must pass guide, tune, heartbeat/recovery, pause/resume/seek-live, one-off/series record, conflict and recordings behavior when connected to a Live TV-capable Vondel Server.

Implementation proceeds in vertical slices, but a partially covered media or platform matrix is not called the initial release.

## Store-review package

Each store submission includes:

- accurate product description and privacy disclosures;
- Vondel-owned support and privacy-policy URLs;
- stable reviewer credentials and a populated demo server;
- explicit review notes describing server connection and all media modes;
- original screenshots/previews showing actual app operation;
- documented rights for all included assets and demo media;
- an originality dossier available if requested.

The originality dossier contains:

- zero-parent empty project roots and full Git history;
- dated design/specification history;
- feature provenance ledger;
- dependency/license inventory;
- clean-room access policy;
- reference-source similarity scan results;
- visual-composition comparison evidence;
- server contract sources and invented fixtures;
- build/test/accessibility/privacy results.

No claim is made that App Review or Google Play approval can be guaranteed. The dossier demonstrates that the applications are independently created and provide original value; reviewers retain discretion under current policies.

## Licensing

The new contracts and client repositories use `AGPL-3.0-or-later`, consistent with the Vondel ecosystem. Their AGPL license does not depend on or imply incorporation of the reference client source. Every third-party dependency and asset must have a recorded compatible license before entering a release build.

## Delivery sequence

1. Create the contracts repository and disposable-server conformance harness.
2. Create empty Apple and Android repositories with originality/provenance guards.
3. Establish original design tokens, navigation shells and server connection flows on all targets.
4. Complete TV-first Watch vertical slices on tvOS and Android TV while maintaining compiling/tested shells elsewhere.
5. Add Listen, including music and audiobooks, across all targets.
6. Add Read, including ebooks and manga, across all targets.
7. Complete phone/tablet/macOS Watch experiences and shared cross-media Now behavior.
8. Complete downloads, recovery, accessibility, localization, privacy and store metadata.
9. Complete the capability-gated Live TV/DVR vertical slice on every target after the Vondel Live TV contracts are published.
10. Run the full platform/media/Live TV acceptance matrix and prepare reviewer environments/dossiers.

Tasks may run concurrently when they touch independent repositories or media coordinators, but conformance and identity-scoping gates remain prerequisites for feature integration.

## Acceptance criteria

The clean-room client project is ready for store submission only when:

- all three new repositories begin from empty Vondel roots and contain no reference source/assets;
- Apple and Android use independently designed native architectures and interfaces;
- the six library media categories and capability-gated Live TV/DVR work across the seven target form factors;
- both Vondel and capability-compatible official Silo servers pass conformance;
- TV focus/navigation and all playback/reader matrices pass;
- offline bundles, profile scoping, retries and diagnostics pass safety tests;
- accessibility, localization, privacy and security gates pass;
- store metadata and visual assets are original and rights-cleared;
- the originality dossier has no unexplained similarity findings;
- reviewer demo accounts and server remain stable for the entire review period.
