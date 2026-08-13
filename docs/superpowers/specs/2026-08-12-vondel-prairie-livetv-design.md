# Vondel Prairie Live TV and DVR Design

**Date:** 2026-08-12  
**Status:** Approved design  
**Source baseline:** `Prairie-Server/prairie-server@095ecd22fbea3384a905eb9049386015db3ff4d8`

## Objective

Add a complete server-native Live TV and DVR subsystem to Vondel by selectively porting Prairie Server's AGPL-3.0 implementation. Vondel will support native HDHomeRun/OTA tuners and Dispatcharr's HDHomeRun-compatible interface for IPTV provider management. It will own channel lineups, guide synchronization, live playback, transcoding, timeshift, tuner arbitration, DVR scheduling, recordings and client contracts.

Every new native Vondel client target supports Live TV at initial store release:

- Apple TV;
- iPhone;
- iPad;
- macOS;
- Android TV;
- Android phone;
- Android tablet.

No Prairie or Silo native-client code, assets, layouts, architecture or tests enter the clean-room Vondel client repositories. Only documented Vondel Live TV contracts and invented fixtures cross the boundary.

## Licensing and provenance

Prairie Server is an AGPL-3.0 fork of Silo Server. Vondel may adapt its server-side Live TV subsystem under the license, but must preserve copyright/license notices and provide accurate attribution.

Before porting, Vondel records:

- the exact Prairie commit above;
- every imported source path and blob hash;
- every Vondel modification;
- files omitted as unrelated to Live TV;
- migration ordering and conflict resolutions;
- the final Vondel commits containing the port.

Vondel's `NOTICE` credits Prairie Server and links the exact source revision. The Prairie baseline is added as a fetch-only audit remote or a detached temporary source checkout; it is never configured as a push target.

## Port boundary

The port is limited to Prairie's Live TV slice and the minimum Vondel integration points it requires:

- `internal/livetv/**`;
- Live TV REST handlers;
- Jellyfin-compatible Live TV handlers;
- guide-sync task registration;
- Live TV database migrations;
- required router, dependency-injection, settings, policy, playback and event integrations;
- Live TV admin, guide and web-player surfaces;
- Docker host-network override and operator documentation;
- focused tests and deterministic fixtures for these components.

Prairie's unrelated commits and features are not merged. Each imported unit is adapted to Vondel's current architecture and tested at its boundary. Existing Vondel behavior and public defaults remain unchanged unless this specification explicitly requires a Live TV capability or operator setting.

## Provider boundary

Vondel does not directly manage arbitrary M3U, Xtream or IPTV-provider credentials in this phase.

- Physical OTA/network tuners use native HDHomeRun discovery and control.
- IPTV provider configuration, authentication and playlist normalization remain in Dispatcharr.
- Dispatcharr exposes its HDHomeRun-compatible discovery, lineup and stream endpoints to Vondel.
- Vondel treats Dispatcharr as a tuner and owns downstream guide matching, playback sessions, timeshift and DVR.
- Dispatcharr and tuner endpoints are expected to be network-private and protected with network ACLs.

This keeps high-risk provider credentials and provider-specific behavior outside Vondel while presenting one normalized tuner model to the server and clients.

## Capability model

Live TV is exposed through a versioned server capability. At minimum, capability metadata identifies support for:

- tuner discovery and management;
- channel lineup;
- guide windows;
- live playback plans;
- timeshift;
- DVR one-off rules;
- DVR series rules;
- recording conflicts;
- completed recordings.

Clients must capability-probe before presenting a Live TV interface. Vondel enables the capability only when the subsystem is available. Capability-compatible official Silo servers work normally; servers without Live TV simply hide the feature without errors or dead navigation.

## Tuner discovery and onboarding

### LAN discovery

Vondel broadcasts SiliconDust discovery packets over UDP port `65001` and listens for HDHomeRun-compatible replies. Dispatcharr instances that expose HDHomeRun UDP discovery may appear alongside hardware tuners.

LAN discovery depends on the host network stack. Linux container deployments use an explicit Live TV Compose override with host networking. Docker Desktop cannot reliably bridge host-network UDP discovery to the physical LAN, so operators use URL probing or run Vondel directly on the host.

### URL probing

Manual onboarding accepts one HTTP(S) base URL. Vondel probes the Prairie-compatible locations:

- `{base}/hdhr/discover.json`;
- `{base}/discover.json`;
- the conventional Dispatcharr port/path when the supplied base permits it.

The server reads device identity and normalized base URL from the verified discovery response. Clients never submit or invent tuner device IDs.

### Verification and persistence

Discovered candidates are HTTP-verified before presentation. Existing tuners are marked rather than duplicated. Add/remove/update operations are administrator-only. Stored tuner identities are unique and stable across rediscovery.

## Fetch and SSRF policy

All tuner, guide, artwork and stream fetches share one explicit outbound policy:

- only `http` and `https` schemes;
- no URL userinfo or credentials;
- bounded URL, hostname and response sizes;
- DNS resolution checked before connection;
- redirect destinations revalidated independently;
- DNS rebinding mitigated by binding policy to the resolved destination used by the connection;
- loopback/link-local/private ranges allowed only under an explicit Live TV LAN policy;
- cloud metadata, multicast control and prohibited special-use destinations denied;
- request, connect, response-header and body timeouts;
- response-size and decompression bounds;
- sanitized errors and logs that never include tokens, credentials or signed stream URLs.

The policy is applied consistently to discovery, lineup, guide, artwork and stream requests. No component may use an unguarded default HTTP client.

## Channel lineup and guide

Tuner channels are normalized into stable Vondel channel identities while preserving source tuner/channel identifiers for refresh and failover.

Guide sources support the Prairie baseline:

- Schedules Direct;
- Gracenote integration where configured and licensed;
- Prairie-compatible XML guide synchronization.

Guide credentials are encrypted at rest with Vondel's existing secret envelope system and never returned after write. Matching uses normalized callsigns, channel numbers and source identities, with administrator overrides for ambiguous results.

Guide refresh is transactional:

1. fetch and validate the source into staging;
2. normalize channels, programs, artwork and time ranges;
3. calculate mappings and conflicts;
4. atomically publish the new guide snapshot;
5. retain the last known-good guide when any required stage fails.

Refreshes are idempotent and bounded by source/time window. Stale guide state is visible to administrators and clients.

## Live playback and tuner arbitration

Live playback flow:

1. authenticated client selects a channel;
2. Vondel evaluates profile policy, channel access and concurrent-stream limits;
3. the scheduler chooses an available tuner and acquires a fenced lease;
4. Vondel selects direct stream, HLS bridge or transcode based on tuner capabilities and client playback capabilities;
5. the server returns a short-lived playback plan/token;
6. client heartbeats retain the lease;
7. disconnect/expiry reclaims the session safely.

Lease ownership uses unique session/fencing tokens so a late heartbeat or worker cannot reclaim a tuner from a newer session. Multiple viewers share a source only when the source and route are explicitly safe to multiplex; otherwise each consumes tuner capacity.

Tuner selection is deterministic and considers availability, source/channel compatibility, health, active recordings and configured preference. Recordings cannot be silently displaced by ordinary live viewing.

## Playback routes

Supported source routes include tuner MPEG-TS and HLS-compatible streams exposed by HDHomeRun/Dispatcharr.

Vondel may return:

- direct tuner/source playback when the client and security model permit it;
- a Vondel HLS bridge that keeps protected tuner URLs server-side;
- a Vondel transcode plan using existing playback/transcode infrastructure.

The HLS bridge rewrites and validates playlists, constrains segment origins, preserves short-lived authorization and never exposes internal provider credentials. Transcoding uses Live TV-specific codec settings derived from tuner and client capabilities.

## Timeshift

Each eligible live session may own a bounded rolling buffer:

- pause and resume live content;
- seek behind the live edge within the retained window;
- return to live;
- recover after a brief client/network interruption when the lease remains valid.

Server policy controls per-session duration, byte limits, total disk budget and user eligibility. Timeshift data is temporary, session-scoped and automatically removed on expiry. It never appears as a recording and cannot cross server/account/profile boundaries.

Disk-pressure behavior is deterministic: shrink or reject new timeshift capacity before threatening finalized recordings or unrelated Vondel state.

## DVR scheduling and recording

Vondel supports:

- record-current/one-off program rules;
- future one-off rules;
- series rules;
- configurable pre/post padding;
- cancellation;
- conflict reporting and deterministic resolution;
- recording ownership/profile scope;
- restart recovery.

The scheduler expands rules against guide programs and reserves tuner capacity. Conflicts expose the programs, rules, tuner capacity and chosen winner; they are never resolved invisibly.

Recording flow:

1. claim a scheduled recording with a fenced execution token;
2. acquire the tuner/session;
3. write the stream to a staging path;
4. monitor lease, disk and source health;
5. finalize metadata and file atomically;
6. place a successful recording into a dedicated Vondel recordings library;
7. retain diagnostic state for failed/partial recordings without presenting them as complete.

Process restarts reconcile scheduled, active and staging records without duplicating jobs or overwriting completed files.

## Data model and migrations

The selective port adapts Prairie's Live TV migrations to Vondel's migration sequence. The model includes, as required by the port:

- tuners and capabilities;
- channels and source mappings;
- guide sources and encrypted credentials;
- guide snapshots/programs/artwork;
- active tuner sessions and heartbeats;
- recording rules and expanded schedules;
- conflicts;
- recording executions/files/status;
- timeshift/session accounting.

Migration tests cover both an empty database and upgrade from the current Vondel schema. Destructive rollback is not used for operator recordings or the last known-good guide. If a migration cannot complete safely, startup fails before partially enabling Live TV.

## API and client contracts

`vondel-client-contracts` adds versioned, invented schemas/fixtures for:

- Live TV capability discovery;
- channels and channel groups;
- guide windows and programs;
- program/channel artwork;
- live playback plans;
- session heartbeat/stop;
- timeshift window and seek state;
- recording rules;
- schedules and conflicts;
- completed/failed recordings;
- typed Live TV errors and stale-guide state.

Fixtures use invented stations, programs and identifiers. They are derived from Vondel server behavior, not Prairie/Silo client source.

Native clients implement original interfaces from these contracts:

- TV-first guide grid, channel browsing, now/next and player overlays;
- touch/keyboard adaptations on phone, tablet and macOS;
- timeshift controls and live-edge indication;
- record/cancel/series rule flows;
- conflict resolution and recordings library.

No Prairie or Silo native-client repository is consulted or copied during implementation.

## Error model

Live TV errors are typed and stable enough for clients to give actionable recovery:

- tuner offline/unreachable;
- no tuner capacity;
- channel unavailable;
- encrypted/DRM channel unsupported;
- guide unavailable or stale;
- source codec unsupported;
- bridge/transcode failure;
- timeshift unavailable/window expired;
- recording conflict;
- disk budget exhausted;
- recording partial/failed;
- policy/authorization denied;
- invalid or unsafe source URL.

Errors sent to non-admin users omit tuner/provider URLs and internal topology. Administrative diagnostics remain redacted and bounded.

## Web administration and playback

Vondel's web application receives the selectively ported Prairie Live TV management and user surfaces, adapted to Vondel's current design system and APIs:

- tuner discovery, probe, add/remove and health;
- guide source configuration and mapping;
- transcode/timeshift/DVR settings;
- guide grid and on-now view;
- live player;
- recording rules, schedules, conflicts and recordings.

The web UI is server functionality and is covered by Prairie attribution. It does not cross into the clean-room native-client repositories.

## Testing strategy

### Port provenance

- exact Prairie tree/file hashes;
- per-file classification: imported, adapted, omitted or Vondel-created;
- preserved AGPL/NOTICE attribution;
- no unrelated Prairie feature files;
- upstream audit remote fetch-only/push-disabled.

### Deterministic source simulators

Tests use fake HDHomeRun and Dispatcharr servers implementing discovery, lineup and live streams. Fixtures cover malformed packets, duplicate devices, timeouts, redirects, channel changes, tuner exhaustion, abrupt disconnects and source recovery.

### Security

Tests cover SSRF, DNS rebinding, redirect revalidation, URL userinfo, cloud metadata destinations, oversized/decompression-bomb guide responses, malformed discovery packets, credential/log leakage, session theft, lease exhaustion, path traversal, symlink writes and cross-profile recording access.

### Guide and DVR

Tests cover transactional refresh/last-good retention, matching overrides, overlapping programs, one-off/series rules, padding, deterministic tuner conflicts, cancellation, process restart, late workers, partial files, disk pressure and atomic finalization.

### Playback and timeshift

Tests cover direct MPEG-TS/HLS, playlist/segment validation, transcoding, supported audio/subtitle variants, pause/resume/seek/live edge, heartbeat expiry, session recovery and tuner reclaim.

### Migration and integration

- clean-database migration;
- upgrade from current Vondel schema;
- full HTTP API against PostgreSQL;
- web guide/player behavior;
- disposable fake-tuner end-to-end playback and recording;
- no persistent database, sessions or media files after test cleanup.

### Client matrix

The clean-room client acceptance matrix adds guide, tune, playback, pause/resume/seek-live, record/cancel, series rule, conflict and recordings tests for all seven targets. A capability-negative official Silo baseline proves Live TV remains hidden when unavailable.

## Delivery order

1. Pin and inventory the Prairie source slice; update Vondel attribution.
2. Port/adapt database migrations and store layer.
3. Port secure fetch policy, HDHomeRun/Dispatcharr discovery and tuner management.
4. Port guide providers, matching, artwork and transactional sync.
5. Port tuner arbitration, sessions, bridge/transcode and timeshift.
6. Port DVR scheduling, recording, conflict and library integration.
7. Port/adapt REST and Jellyfin-compatible handlers.
8. Port/adapt Vondel web administration, guide and live player.
9. Add contracts/fixtures and native-client vertical slices without client source reuse.
10. Run provenance, security, migration, server/web integration and full client matrices.

## Acceptance criteria

The Live TV subsystem is complete when:

- the exact Prairie source baseline and every imported/adapted file are recorded;
- Vondel NOTICE/AGPL obligations are satisfied;
- HDHomeRun and Dispatcharr discovery/probing work under the documented network modes;
- channels, guide providers/matching, artwork and last-good refresh work;
- live direct/bridge/transcode playback, timeshift and session recovery work;
- one-off/series DVR, conflicts, restart recovery and recordings-library integration work;
- SSRF, secret, lease, disk, path and profile-boundary gates pass;
- fresh and upgrade migrations pass;
- Vondel web administration/guide/player pass;
- Live TV contracts contain only invented data and documented behavior;
- all seven clean-room client targets pass the Live TV matrix;
- official Silo servers without the capability degrade by hiding Live TV;
- no Prairie or Silo native-client source or assets exist in Vondel client repositories.
