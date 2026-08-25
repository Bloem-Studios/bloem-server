# Bloem External Compatibility Applications Design

**Original date:** 2026-08-12

**Revised:** 2026-08-13

**Status:** Approved

**Scope:** Extract the embedded Jellyfin and Audiobookshelf compatibility surfaces from Bloem Server into independently deployed, removable applications.

## Decision

Bloem Server remains the authoritative media server. Jellyfin and Audiobookshelf compatibility become two optional applications:

- `bloem-jellyfin`
- `bloem-audiobookshelf`

They translate external protocols into a private, versioned Bloem Compatibility Service API. They do not read or write Bloem's database, Redis, media filesystem, Docker socket, signing secrets, or provider credentials. Removing either application must leave native Bloem and all canonical user media state intact.

Both applications use the same public address as Bloem:

- Native Bloem API and Web: `https://bloem.example/`
- Jellyfin clients: `https://bloem.example/`
- Jellyfin Web: `https://bloem.example/web`
- Audiobookshelf clients: `https://bloem.example/audiobookshelf`

Bloem's normal HTTPS listener owns public ingress and forwards only an audited, fixed set of compatibility paths. The companion containers expose no host HTTP ports by default.

This extraction applies only to compatibility facades. Native audiobook, ebook, manga, comic, podcast, movie, series, music, radio, Live TV, playback, and library behavior remains in Bloem Server. Plex import/sync and ARR webhooks remain outside this extraction.

## Goals

- Make Jellyfin and Audiobookshelf compatibility separately installable, upgradeable, disableable, and removable.
- Preserve observable behavior required by official and widely used third-party clients.
- Use one canonical Bloem address without fragile client-header detection.
- Make Bloem the sole authority for authentication, profiles, tenancy, authorization, media state, playback, and Live TV.
- Preserve legacy account login while adding optional direct profile login.
- Enforce organization, profile, adult-content, device, and policy boundaries identically across native and compatibility clients.
- Give each application an independent failure, credential, persistence, and release boundary.
- Support same-host Compose by default and authenticated remote placement for larger installations.

## Non-Goals

- Move native media-domain behavior into a compatibility application.
- Give a compatibility application direct database or filesystem access.
- Preserve active compatibility tokens during extraction; one fresh client login is acceptable.
- Give Bloem control of the Docker socket or an equivalent host-management capability.
- Guess a protocol from user-agent strings, request bodies, or other mutable client hints.
- Expose a public profile directory.
- Make the compatibility applications public repositories or artifacts.

## Architecture

### Public ingress

Bloem remains the only public HTTP service. Its edge gateway routes requests through a version-controlled allowlist:

```text
Client
  |
  +-- native paths ------------------------------> Bloem native handlers
  |
  +-- Jellyfin protocol paths --> bloem-jellyfin --> private Bloem API
  |
  +-- /audiobookshelf/** -------> bloem-audiobookshelf --> private Bloem API
```

Native routes include `/api/v1/**`, `/api/v2/**`, and the Bloem application at `/`. Jellyfin owns `/web` while enabled plus its explicit protocol route families for system, users, items, sessions, and Live TV. Audiobookshelf owns only `/audiobookshelf/**`; the gateway strips the fixed prefix when required by its protocol adapter.

The route table is data-driven, reviewed, and tested for overlap. Unknown paths never fall from one authentication surface into another. The gateway strips hop-by-hop headers, applies request-size and timeout limits, propagates a trace identifier, and supplies a signed internal request identity. A companion cannot add a route at runtime.

When an application is missing, disabled, revoked, unhealthy, or API-incompatible, only its paths return a protocol-appropriate `compatibility_unavailable` response. Native Bloem remains healthy.

### Bloem Server

Bloem owns:

- accounts, profiles, credentials, device trust, sessions, organization membership, and policy;
- libraries, catalog, search, metadata, artwork, collections, playlists, favorites, bookmarks, and recommendations;
- progress, watch/read/listen state, downloads, and activity;
- playback planning, direct streams, transcode orchestration, subtitles, and signed media delivery;
- Prairie-derived Live TV tuners, guide, streams, recordings, DVR rules, and recording files;
- canonical events, auditing, revocation, and capability discovery.

The server exposes a private Compatibility Service API. It does not expose SQL concepts, unrestricted filesystem paths, raw provider/tuner credentials, master signing material, or unrelated administrative mutation.

### Compatibility applications

Each application:

- implements exactly one external protocol;
- translates protocol requests and responses without owning media-server policy;
- authenticates with its own scoped service identity;
- exchanges external user credentials with Bloem without storing, hashing, or logging them;
- stores only disposable protocol state;
- consumes subject-filtered Bloem events with resumable cursors;
- returns same-origin, short-lived Bloem delivery URLs for media whenever its client protocol permits;
- has no dependency on the other compatibility application.

Media bytes must not make a needless round trip through a companion. The companion requests a device- and audience-bound delivery grant; the client then fetches the same-origin Bloem URL. Proxying through a companion is allowed only when its external protocol requires proxy semantics and must be bounded, cancellable, and slow-client tested.

## Identity and Authentication

### Account and profile identities

Accounts remain the ownership and administration identity. Profiles remain the media-consumption identity.

A profile has exactly one of two credential states:

1. **Shared-only:** display name and policy state; no email and no password.
2. **Direct-login enabled:** a unique email and password are both present.

Partial credentials are invalid. Email is optional for a shared-only profile. Emails used for direct login are globally unique across accounts and profiles so login is never ambiguous.

Per-profile switching PINs retain their current semantics. A PIN protects profile selection on an already trusted shared device; it is not a reusable remote-login credential.

### Login flows

- **Direct profile login:** Profile email/password yields a session already bound to one organization, account, profile, and device. No sibling profiles are disclosed.
- **Legacy account login:** Account username/email and password preserve Silo compatibility. The device selects its remembered or default profile and may enumerate only profiles authorized for that account or trusted device.
- **Shared-device switching:** A revocable device grant contains the profile IDs permitted on that device. An unprotected profile switches without another password. A protected profile requires its existing PIN.
- **Unknown Jellyfin device:** The public user list is empty. After account/device authentication, only the device's authorized profile tiles are returned.
- **Audiobookshelf:** Direct profile login is preferred. Legacy account login resolves the remembered/default profile because the protocol has no reliable general-purpose profile picker.

Companions see a submitted password transiently because they terminate the external protocol request and immediately forward the credential exchange to Bloem. They must not persist, hash, cache, inspect beyond required protocol decoding, or log the password. Transport between a remote companion and Bloem is mutually authenticated and encrypted.

Every effective compatibility session is bound to:

- organization, account, profile, and device IDs;
- authentication method and companion audience;
- granted capabilities;
- membership, security, account-policy, and organization-policy revisions;
- expiry and revocation identity.

Bloem reauthorizes at its API boundary. Profile or organization suspension, password reset, policy revision, device revocation, or companion revocation invalidates access without trusting companion cache state.

## Compatibility Service API

The private API uses versioned HTTP/JSON with a generated OpenAPI contract. It supports:

- service enrollment, capability negotiation, health, and version compatibility;
- credential exchange, device trust, profile discovery, PIN verification, and profile switching;
- libraries, catalog browse/search/detail, people, chapters, seasons, episodes, and metadata;
- artwork and signed resource resolution;
- playback planning, direct-stream/transcode authorization, cancellation, and recovery;
- progress, watched state, favorites, bookmarks, collections, playlists, and downloads;
- live sessions and device reporting;
- Live TV channels, guide, tuner availability, stream authorization, DVR rules, and recordings;
- subject-filtered events with reconnect cursors and bounded resynchronization.

Subject-scoped tokens determine organization, account, profile, and device. A companion cannot provide arbitrary subject identifiers to impersonate another user. Mutations require idempotency keys. Lists use signed cursors. Stream grants are short-lived and single-purpose.

Adult-content policy is enforced before catalog entries, search hits, counts, recommendations, activity, artwork, events, or streams are returned. Unauthorized subjects receive non-disclosing negative results without timing or identifier hints.

## Service Trust

Each companion has a separate immutable instance identity and capability grant.

1. An administrator creates a short-lived, single-use enrollment token.
2. Same-host Compose mounts it as a Docker secret, never an environment variable.
3. The companion registers its identity, API range, version, and requested capabilities.
4. Bloem grants only the reviewed capabilities and issues renewable short-lived service credentials.
5. The enrollment token is destroyed after use.

Same-host deployments use a private container network plus application-layer service authentication. Remote deployments additionally require mutually authenticated HTTPS. Credentials are independently rotatable and revocable. Compromise of one companion must not grant administration, another companion's capabilities, or arbitrary user access.

## Data Ownership and Retention

Canonical state remains in Bloem, including progress, bookmarks, favorites, collections, playlists, downloads, Live TV recordings/rules, and all identity/policy state.

Companions may persist only protocol-specific state such as:

- client access-token correlation;
- device capability descriptions;
- Jellyfin display preferences;
- Socket.IO/WebSocket bookkeeping;
- protocol translation caches and resumable event cursors.

This state is disposable. The default deployment uses SQLite in a companion-owned named volume with WAL enabled. Replicated or larger deployments may use a separate PostgreSQL database owned solely by that companion. A companion may share a PostgreSQL server, but never Bloem's database, schema, or database credential.

Disabling a companion preserves its volume. Explicit uninstall may remove it. Reinstallation can require login and reset client-specific presentation preferences, while canonical Bloem state reappears unchanged.

## Deployment and Configuration

The primary Compose file contains clear commented examples for both optional applications. Complete override files provide the supported activation path:

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.jellyfin.yml \
  up -d
```

The companion services expose no host HTTP ports by default. An explicitly documented diagnostic override may bind loopback-only ports. Direct public companion exposure is unsupported.

Examples may use the `latest` stable channel. Every companion performs an API-range handshake before readiness, and an incompatible image fails closed. Bloem displays the resolved version and image digest. Operators who require deterministic change control may pin a version or digest.

Bloem never mounts or accesses the Docker socket. The admin UI reports an available update and supplies exact Compose update/rollback commands; the operator or an external deployment controller executes them.

### Admin experience

Admin -> Compatibility Applications displays:

- installed, enabled, disabled, unhealthy, revoked, or incompatible state;
- instance identity, application version, resolved image digest, and API range;
- last contact, health, active client sessions, and granted capabilities;
- enrollment, enable/disable, credential rotation, and revocation controls;
- the canonical client URL to copy;
- installation, update, rollback, and removal instructions.

Starting a container does not automatically claim public routes. An administrator must enroll it and explicitly enable routing after Bloem verifies health and API compatibility.

## Jellyfin LAN Discovery

Bloem's edge gateway provides a small optional Jellyfin discovery relay because companions have no public ports.

- It responds only while an enrolled Jellyfin companion is enabled, healthy, and compatible.
- It advertises the canonical Bloem HTTPS address and stable Jellyfin-compatibility server ID.
- It discloses no users, profiles, organizations, internal addresses, or topology.
- Responses are rate-limited to avoid amplification abuse.
- Operators may bind selected interfaces or disable discovery.
- Manual server-address entry always remains supported.

Audiobookshelf continues to use its explicit `https://bloem.example/audiobookshelf` address.

## Failure Behavior

- Bloem starts and serves native clients with zero, one, or both companions absent.
- Companion health is circuit-broken; failures do not cascade into native handlers.
- Unsupported API versions fail closed with an actionable operator error.
- Event gaps cause bounded resynchronization using a durable cursor.
- Deadlines and playback cancellation propagate across the boundary.
- Existing Bloem-issued media deliveries may finish after companion failure; no new authorization is issued through an unhealthy companion.
- WebSocket and Socket.IO clients receive protocol-appropriate closure and may reconnect after recovery.
- Logs share a trace identifier and redact credentials, cookies, tokens, signed URLs, and sensitive query data.

## Extraction and Cutover

1. Freeze the observable embedded Jellyfin and Audiobookshelf behavior with protocol fixtures and black-box acceptance tests.
2. Define and implement the minimum Compatibility Service API required by those fixtures.
3. Create private `bloem-audiobookshelf` and `bloem-jellyfin` repositories with appropriate provenance and license notices.
4. Extract Audiobookshelf first because it is smaller and already has the collision-free `/audiobookshelf` public boundary.
5. Run the embedded and external Audiobookshelf implementations against the same suite, then switch only its route.
6. Extract Jellyfin by domain: identity, catalog, playback, sessions/events, Live TV, then Jellyfin Web.
7. Switch the fixed Jellyfin route table only after the external application passes the full suite.
8. Require one fresh client login at each cutover. Do not migrate active compatibility tokens or reusable credentials.
9. Remove embedded listeners, handlers, installers, implementation packages, compatibility-specific wiring, and direct compatibility database access from the Bloem binary.
10. Retain old protocol-state tables as inert rollback data for one release, then remove them in a later migration. Canonical media state is never removed.

There are no shared-database dual writes. Bloem remains authoritative throughout.

## Verification and Release Gates

The extraction is complete only when automated black-box tests cover:

- direct credentialed-profile login;
- legacy account login and remembered/default profile selection;
- unknown-device non-disclosure, trusted-device profile tiles, and per-profile PIN switching;
- cross-account and cross-organization isolation;
- immediate profile, membership, organization, device, password, policy, and companion revocation;
- adult-content absence across metadata, search, counts, artwork, recommendations, activity, events, and playback;
- libraries, browse, search, detail, progress, favorites, bookmarks, collections, playlists, and downloads;
- playback planning, direct play, transcoding, seeking, subtitles, cancellation, recovery, and session reporting;
- Jellyfin Live TV discovery, guide, streaming, DVR rules, recording, and recordings;
- Audiobookshelf playback, offline/download metadata, progress, bookmarks, and Socket.IO;
- event reconnect, gaps, companion restart, Bloem restart, slow clients, and unavailable dependencies;
- credential rotation, incompatible API ranges, disabled routing, and companion removal;
- official client behavior where automation is practical, backed by deterministic captured protocol fixtures elsewhere.

Additional structural gates prove:

- both companions have no Bloem database credential, Redis credential, media-data mount, Docker socket, or signing secret;
- only fixed public paths can reach a companion;
- native Bloem conformance remains green with both companions stopped;
- the final Bloem binary contains neither embedded compatibility implementation;
- uninstalling a companion loses only disposable protocol state;
- source, image, log, and redirect scans do not leak credentials or signed resources.

### Foundation gate status

The foundation (Tasks 1-6 plus the required CI gate in Task 7 of
`docs/superpowers/plans/2026-08-13-bloem-compatibility-1-foundation.md`) is
implemented. Extraction itself — the two companion repositories, the actual
route cutover, and everything else in Extraction and Cutover above — remains
not started. `internal/acceptance/compat_foundation_test.go`
(`TestCompatibilityFoundation`, required in CI as the `compat-foundation`
job) proves, against one disposable PostgreSQL database and the real
enrollment service, private API, gateway, and admin adapter:

- enrollment, incompatible-API-range rejection, and idempotent replay
  (including same-key/different-body conflict);
- fixed-path routing to the owning companion with prefix stripping and
  signed-identity/trace header propagation, cross-companion isolation, and
  native routes never reaching the gateway;
- revoked and disabled applications answering unavailable without ever
  forwarding a request, and the native surface answering with both
  companions stopped;
- direct-profile login, per-request subject revalidation, signed-cursor
  round-trip plus rejection of bit-tampering and cross-operation reuse, PIN
  verification, profile switching, and revocation invalidating every
  outstanding subject token.

The `compat-compose-structural-scan` CI job runs the default-deny
`scripts/verify-compat-compose.sh` scan (and its own violation-detection
self-test) on every PR, which it did not before Task 7.

Two production seams the foundation still lacks, both already recorded at
their call sites and in the acceptance file's header, are unaffected by this
gate and remain open follow-up work: `cmd/silo` never sets
`api.Dependencies.CompatAPIV1`, so the private compatibility API is
unreachable in a running server; and no production `SubjectService` or
`CatalogService` adapter exists, so login/profile/catalog behavior is proven
against the real `compatapi` handler code with a working test-double
identity and catalog provider, not against Bloem's real password
verification or adult-content policy. Adult-content non-disclosure and the
private API's own disabled-route behavior are not yet covered for the same
reason and remain open.

## Documentation Deliverables

- Operator guide for enabling, enrolling, updating, rolling back, disabling, and uninstalling each companion.
- Jellyfin client guide using the canonical Bloem address and LAN discovery.
- Audiobookshelf client guide using the `/audiobookshelf` suffix.
- Profile login guide covering direct credentials, legacy accounts, trusted devices, and per-profile PINs.
- Remote companion guide covering mTLS, firewalling, credential rotation, and health checks.
- Troubleshooting matrix for unavailable, incompatible, revoked, and unhealthy states.
