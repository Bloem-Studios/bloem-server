# Vondel Compatibility Sidecars Design

**Date:** 2026-08-12

**Status:** Approved

**Scope:** Extract the embedded Jellyfin and Audiobookshelf compatibility surfaces from Vondel Server into independently deployed private sidecars.

## Decision

Vondel Server remains the authoritative media server. Jellyfin and Audiobookshelf compatibility become two separate services:

- `vondel-compat-jellyfin`
- `vondel-compat-audiobookshelf`

Each sidecar translates its external protocol into a private, versioned Vondel module API. Sidecars do not read or write the Vondel database directly and do not own authoritative media, identity, permission, playback, or progress state.

This extraction applies only to compatibility facades. Native media support remains in Vondel Server. Plex import/sync and ARR webhooks remain in the server during this phase.

## Existing Native Media Boundary

The extraction must preserve Vondel's existing native implementations:

- Audiobooks: dedicated scanning, metadata enrichment, authors, narrators, series, chapters, playback, progress, collections, and the native web player.
- Ebooks: EPUB, PDF, MOBI, AZW, AZW3, FB2, FBZ, CBZ, and CBR scanning; metadata enrichment; conversion; reading progress; reader configuration; annotations; and the native web reader.
- Manga: first-class series, linked readable chapters, volumes, progress, enrichment, and manga-specific web views.
- Comics: CBZ and CBR reading works through the ebook reader. A later core-media change must make Western comics a consistent first-class library classification rather than treating `comics` as an incomplete alias.
- Podcasts: local scanning and feed synchronization foundations.

None of these native capabilities move into a compatibility sidecar. The current `internal/audiobooks/abs`, `internal/audiobooks/abssocket`, and `internal/jellycompat` implementations are extraction sources, not evidence that audiobook or general media domain ownership belongs outside the server.

## Goals

- Remove Jellyfin and Audiobookshelf listeners, protocol handlers, compatibility configuration, and compatibility-specific web component management from the Vondel binary.
- Preserve protocol behavior for existing Jellyfin and Audiobookshelf clients.
- Keep Vondel's native API and clients independent of sidecar availability.
- Give each compatibility surface an independent release and failure boundary.
- Enforce Vondel profile, library, and content restrictions consistently, including adult-content isolation.
- Prevent sidecars from becoming alternate sources of truth or privileged database clients.

## Non-Goals

- Reimplement native audiobook, ebook, manga, comic, podcast, movie, series, music, radio, or Live TV domain logic in sidecars.
- Extract Plex import/sync, ARR webhooks, or general plugin capabilities in this phase.
- Make compatibility services public repositories or public artifacts.
- Preserve undocumented implementation details that are not observable protocol behavior.
- Allow a sidecar to bypass Vondel authorization, profile selection, policy, or audit logging.

## Architecture

### Vondel Server

Vondel remains authoritative for:

- users, profiles, authentication, device sessions, and permissions;
- libraries, catalog, metadata, artwork, collections, recommendations, and search;
- progress, favorites, watchlists, reading/listening state, and activity;
- playback planning, transcode orchestration, direct streams, downloads, and signed media access;
- events and capability discovery;
- adult-library authorization, explicit profile opt-in, PIN state, and exclusion from unauthorized discovery.

Vondel exposes a private module API with an explicit version. It is narrower than the native client API and contains only the operations required by compatibility adapters.

### Compatibility Sidecars

Each sidecar:

- listens on its own port or hostname;
- implements one external protocol;
- authenticates to Vondel with its own scoped service identity;
- maps external users and sessions to Vondel identities and profiles;
- translates requests and responses without taking ownership of media state;
- consumes scoped Vondel events for protocol features that require live updates;
- stores only ephemeral caches, protocol session correlation, and revocable credentials.

Jellyfin clients connect to `vondel-compat-jellyfin`. Audiobookshelf clients connect to `vondel-compat-audiobookshelf`. Vondel's native clients never depend on either service.

### Media Data Flow

For ordinary metadata operations, the sidecar calls the Vondel module API and translates the result. For playback, the sidecar creates or resolves an authorized Vondel playback session. Media bytes flow directly from Vondel when the external protocol permits it. A sidecar proxies bytes only where the protocol requires proxy semantics, and that path remains bounded, cancellable, and covered by slow-client tests.

## Module API

The module API is private, authenticated, and versioned independently from external protocols. Its first version covers:

- service identity and health;
- external-user login/session exchange without exposing stored credentials;
- profile selection and effective access scope;
- library and catalog browse/search/detail;
- artwork and signed resource resolution;
- progress, favorites, collections, and session state;
- playback-plan creation, direct-stream and transcode handoff, cancellation, and recovery;
- scoped events and reconnect cursors;
- capability negotiation so sidecars can fail closed when Vondel lacks a required operation.

The API must not expose SQL concepts, internal table identifiers where stable content identifiers exist, unrestricted filesystem paths, master signing keys, raw provider credentials, or administrative mutation unrelated to the adapter.

## Security and Privacy

- Jellyfin and Audiobookshelf use different service identities and credentials.
- Service permissions are least-privilege and library/profile scoped.
- Every user-visible operation is reauthorized by Vondel; a sidecar assertion alone is insufficient.
- Adult libraries are opt-in per profile and may be PIN locked. Unauthorized profiles receive no adult catalog entries, search hits, recommendations, activity, artwork, counts, timing-based existence hints, or event payloads.
- Sidecars never receive or log a user's reusable Vondel password.
- Logs redact authorization headers, cookies, API tokens, signed URLs, playback tokens, and sensitive query parameters.
- Redirects never forward Vondel credentials to a different origin.
- Sidecars have no PostgreSQL credentials and cannot mount Vondel's data directory.
- A sidecar compromise must not grant Vondel administration or access to the other sidecar's scope.

## Migration

1. Capture observable behavior from the embedded Jellyfin and Audiobookshelf implementations as protocol fixtures and characterization tests.
2. Define the minimal versioned module API needed by those fixtures.
3. Create private sidecar repositories with provenance and license notices for extracted code.
4. Move and adapt the existing compatibility implementations while retaining protocol behavior.
5. Run embedded and sidecar implementations against the same fixtures and development-server acceptance suite.
6. Deploy sidecars on separate ports or hostnames and migrate test clients.
7. Remove embedded listeners, compatibility configuration, Jellyfin web installer commands, compatibility-only wiring, and extracted packages from the Vondel binary.
8. Leave clear migration diagnostics for old listener configuration. Never silently enable, publish, or expose a sidecar.

The migration does not require a shared database or dual writes. Vondel remains authoritative throughout.

## Failure Behavior

- Vondel starts and serves native clients when either or both sidecars are absent.
- A sidecar reports protocol-native unavailable or authentication errors when Vondel cannot fulfill a request; it does not return fabricated stale success.
- Sidecar restarts preserve authoritative sessions and progress through Vondel identifiers and reconnect cursors.
- Event gaps trigger bounded resynchronization through the module API.
- Playback cancellation and caller deadlines propagate across the sidecar boundary.
- Unsupported module API versions fail closed with an actionable operator message.

## Verification

The extraction is complete only when:

- protocol fixtures cover login, users/profiles, libraries, browse, search, item detail, artwork, progress, collections, playback, downloads, WebSockets/events, errors, and logout;
- embedded and sidecar responses match for the characterized compatibility contract before embedded removal;
- end-to-end tests run against the dedicated Vondel development server;
- adult content is absent across all unauthorized compatibility endpoints, images, events, activity, and timing-safe negative responses;
- expired credentials, scope denial, Vondel downtime, sidecar restart, interrupted playback, reconnect, and slow-client cases pass;
- credential and signed-URL leakage scans pass;
- native Vondel server/client conformance remains green with both sidecars stopped;
- no sidecar has a database credential, Vondel data-volume mount, or undocumented privileged endpoint;
- the final Vondel binary no longer contains Jellyfin/Audiobookshelf listeners or compatibility implementation packages.

## Deployment

The supported topology is one Vondel Server plus zero or more compatibility sidecars. Operators explicitly enable and route each sidecar. Containers use private network connectivity to Vondel and expose only the protocol listener required by clients. Sidecar releases remain private indefinitely and are version-pinned in the development and production deployment manifests.
