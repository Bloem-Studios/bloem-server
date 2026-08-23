# Server A–Z architecture evaluation

Status: architecture audit of `origin/main` at `e88a3588` on 2026-08-23.

This review applies the same standard used for the Vondel native clients: find
places where runtime truth has been replaced by declarations, where several
components claim authority over the same decision, and where a local lifecycle
is mistaken for a system lifecycle. It covers the Go API and workers, storage,
playback, Watch Together, compatibility surfaces, plugins, and the bundled React
application.

This is a read-only evaluation. It records verified source findings and an
implementation order; it does not claim that the findings have been repaired.

## Executive verdict

The exact native-client playback-capability defect is **not present in the web
client**. `web/src/player/hooks/useCodecDetection.ts` probes
`MediaCapabilities.decodingInfo`, `MediaSource.isTypeSupported`, and
`HTMLMediaElement.canPlayType`. `web/src/player/client-context-v3.ts` projects
that evidence into protocol v3, and the server consumes the submitted playback
context. Unknown browser facts are generally left unknown instead of being
filled from a device model table. That architecture should be preserved.

The server has a related, more consequential class of authority problems:

- several control planes described as cluster-capable are actually process-local;
- outbound URL security policy has multiple implementations and at least two
  fetch paths bypass all of them;
- runtime configuration is split between the validated config snapshot and
  package-level environment reads;
- legacy and v2 administration use overlapping authority models;
- authentication credentials are exposed in browser persistence and URLs; and
- very large composition and domain files make those ownership boundaries hard
  to see, test, and change safely.

The most urgent defects are not cosmetic refactors. They can cause SSRF,
incorrect fleet-wide admission, split Watch Together rooms, and loss or
divergence of SQLite-backed user state.

## Severity summary

| ID | Severity | Area | Finding |
| --- | --- | --- | --- |
| S-01 | Critical | Security | Authenticated collection artwork import permits unrestricted server-side HTTP fetches |
| S-02 | Critical | Watch Together | live membership and WebSocket fanout are process-local |
| S-03 | Critical | Playback admission | account and tenant stream/transcode counts are process-local |
| S-04 | Critical | User state | supported SQLite user state is node-local; the apparent S3 replicator is unused and non-functional |
| S-05 | High | Admin import | remote catalog seed import permits SSRF, unbounded reads, and double-fetch TOCTOU |
| S-06 | High | Identity/web | bearer and profile credentials travel in URLs; refresh and impersonated-admin credentials persist in `localStorage` |
| S-07 | High | Network policy | outbound request validation and HTTP-client construction have no single authority |
| S-08 | High | Configuration | validated startup configuration is bypassed by package-level environment reads |
| S-09 | High | Authorization | legacy role checks and v2 platform/organization authority coexist at handler level |
| S-10 | High | Modularity | application and domain composition boundaries have become god files |
| S-11 | Medium | Uploads/jobs | resumable upload ownership is process-local and restart destroys resumability |
| S-12 | Medium | Product governance | source and shipped UI include Live TV while repository policy says it is permanently excluded |
| S-13 | Medium | Web maintainability | generated API types and player/admin surfaces have grown into manually coordinated monoliths |

## Critical findings

### S-01 — authenticated collection artwork SSRF

`internal/api/handlers/collection_artwork.go` accepts any syntactically valid
`http` or `https` URL in `downloadCollectionImageURL`. It then uses the supplied
client or `http.DefaultClient`, follows redirects, and downloads the response.
It does not reject credentials, loopback, private, link-local, metadata, or
otherwise special-use destinations, and it does not pin the validated DNS
answer to the dial.

The personal-collection path calls this helper from
`internal/api/handlers/collections.go` after checking collection ownership. It
is therefore reachable by an ordinary authenticated profile that can create or
edit its own collection, not only by a platform administrator. The admin
library-collection artwork path uses the same helper.

The repository already contains stronger but separate implementations in
`internal/imagecache/imagecache.go`,
`internal/notifications/webhook_guard.go`,
`internal/config/theme_url.go`, and `internal/livetv/fetch_url.go`. Their
existence makes the bypass unambiguous: collection artwork is not using the
reviewed network boundary.

Required repair:

1. Introduce one outbound-fetch package that owns URL normalization, resolved
   address classification, redirect revalidation, DNS-rebinding-resistant
   dialing, timeouts, response limits, and content policy.
2. Migrate collection artwork first and add ordinary-profile tests for IPv4,
   IPv6, mapped IPv4, redirects, DNS rebinding, credentials, and cloud metadata.
3. Migrate every other server-side fetch before deleting the local validators.

### S-02 — Watch Together splits across replicas

`internal/watchtogether/service.go` persists the room record and suggestions,
but its active `rooms`, members, socket connections, timers, and dispatch lists
are guarded by a process-local mutex. `getOrLoadLiveRoom` creates a new local
member map on whichever replica receives a request. Snapshot and transport
commands iterate only that local map and call the local connections directly.

Consequences:

- participants routed to different replicas see different member counts and
  readiness barriers;
- controls on one replica are not broadcast to sockets on another;
- host-disconnect timers can disagree and close or preserve the same room
  independently; and
- losing a node drops its entire live membership graph even though the durable
  room survives.

The advisory lock around idle sweeping solves only duplicate janitor work; it
does not make the live room distributed.

Required repair: assign each room a generation-fenced owner with a renewable
lease, route or relay every room command through that owner, publish outbound
frames over a shared bus, and make socket attachment resumable after owner loss.
No replica may independently construct an authoritative `liveRoom` for the same
generation.

### S-03 — playback limits are local, not fleet-wide

`internal/playback/session.go` stores active sessions in an in-memory map.
Admission computes `CurrentActiveStreams`, `CurrentActiveTranscodes`, and the
supposedly shared tenant transcode count from that map. The policy adapter then
receives those local counts. A reconciler writes sessions to
`playback_sessions_sync` for observation, but the admission decision does not
atomically reserve capacity there or in another shared store.

In a cluster, a limit of two transcodes can admit two on every API replica.
Concurrent starts on separate nodes can both observe free capacity and pass.
The comment that `TenantMaxTranscodes` is shared across every account is only
true inside one process.

Required repair: replace count-then-insert with an atomic, leased, shared
reservation. Account, profile, tenant, and hardware-node capacity must be
reserved in one transaction or script, tied to playback generation, renewed by
heartbeats, and released idempotently. The local session map may remain a cache
or execution registry, but not the admission authority.

### S-04 — SQLite user state is not replicated

The repository describes Postgres/S3/Redis and horizontal scale as foundational,
yet `userdb.backend=sqlite` is a supported configuration and the deployment
guide tells operators to persist `/var/lib/silo/userdb`.

`internal/userdb/litestream.go` declares an S3 replicator, but:

- no production code calls `NewReplicator`;
- the configured endpoint, bucket, credentials, and sync interval are not wired
  into `cmd/silo/main.go`'s SQLite provider;
- the purported S3 implementation explicitly only logs, toggles an in-memory
  boolean, and returns success from restore; and
- `internal/userdb/pool.go` directly opens `{DataDir}/{userID}.db` on the local
  node.

An operator can therefore select SQLite, lose the node or move traffic to a
different replica, and lose or fork profiles, progress, preferences, lists, and
other user state. Logging “starting replication” would make diagnosis worse.

Required repair: fail startup when SQLite is selected in a clustered mode, or
implement and prove real single-writer ownership plus restore/replication before
advertising it. Remove the fake replicator immediately; a loud unsupported
configuration is safer than false durability.

## High-severity findings

### S-05 — catalog seed remote import bypasses fetch safety

`readImportDataFromRemoteURL` in
`internal/api/handlers/catalog_seed.go` checks only scheme and a `.json.gz`
suffix. It follows redirects, permits private destinations, and calls
`io.ReadAll` without a maximum. `HandleImport` downloads the URL once as a
preflight and stores the URL in a job; the job downloads it again later. The two
fetches can resolve to different addresses or return different data, and a
large response can exhaust memory twice.

This is admin-reachable rather than profile-reachable, so it is below S-01, but
administrator browsers and API keys remain realistic confused-deputy entry
points. Fetch once through the shared outbound boundary into a bounded,
content-addressed artifact, validate it, and enqueue that immutable artifact.

### S-06 — browser credential exposure is broader than necessary

The web application appends the general access token to native media URLs in
`web/src/player/stream-url.ts`. Watch Together puts room token, general access
token, profile ID, and profile token in its WebSocket query in
`web/src/player/hooks/useWatchTogetherRoomConnection.ts`. The realtime provider
also retains a bearer-token URL fallback even though a short-lived ticket flow
exists.

Query credentials can be captured by ingress logs, diagnostics, browser
history, copied URLs, crash reports, and observability tooling. The server
already has a scoped stream-token design and a WebSocket ticket endpoint; those
patterns should replace general credentials everywhere.

Separately, the SPA retains refresh credentials and, during impersonation, a
full administrator access/refresh pair in `localStorage`
(`web/src/lib/impersonationSession.ts`). Any same-origin script compromise gains
durable administrator recovery credentials. Move refresh and impersonation
recovery to rotated, HttpOnly, Secure, SameSite cookies or a server-side
handoff. Keep access tokens memory-only and make impersonation restoration
single-use and server-bound.

### S-07 — outbound network access has multiple authorities

At least four packages separately decide whether a destination is public, with
different allowances and address handling. Many unrelated packages construct
`http.DefaultClient` or an unconfigured `http.Client` directly. This duplicates
redirect, timeout, proxy, DNS, response-size, TLS, and cancellation decisions.
S-01 and S-05 are the resulting production escapes.

Create named clients from a single composition-owned outbound network service:
public internet, explicitly configured private/LAN integration, loopback-only
internal control, and plugin/provider traffic. Each class should have one
reviewable policy and telemetry. Packages request a class; they do not build a
transport.

### S-08 — configuration is not one immutable authority

Most settings flow through `internal/config`, but production packages still
read environment variables directly, including scanner/enrichment worker
counts, metadata match thresholds, database tuning and migration timeouts,
stream deadlines, task backfill behavior, relay URLs, node identity, and public
URL values. Some of these reads occur when a package is constructed; others can
occur later.

That makes effective configuration dependent on call timing, complicates hot
reload, produces undocumented precedence, and prevents recording one startup
snapshot for incident diagnosis. Move every supported input into a typed,
validated config snapshot. Dynamic settings need a versioned snapshot provider;
static settings must be frozen at boot. Environment access should be confined
to bootstrap and tests.

### S-09 — authorization models overlap

The codebase has a v2 platform/organization authority context and membership
revision model, while many handlers and event filters still decide authority
with `claims.Role == "admin"` or `middleware.IsAdmin`. Legacy compatibility may
need role projection, but business handlers should not independently interpret
it.

The risk is semantic drift during revocation, scoped API-key use, delegated
organization administration, and profile acting context. Consolidate decisions
behind an authorization service that consumes an immutable request principal
and typed action/resource. Legacy and Jellyfin surfaces should translate into
that principal once at their boundary. Direct role checks outside the
authorization package should be prevented by a repository lint test.

### S-10 — composition and domain god files obscure ownership

Representative production files are:

- `internal/metadata/service.go` — 7,486 lines;
- `internal/sections/fetcher.go` — 4,117 lines;
- `internal/catalog/detail.go` — 4,082 lines;
- `internal/api/router.go` — 4,060 lines;
- `internal/scanner/scanner.go` — 3,795 lines;
- `internal/api/handlers/library_collections.go` — 3,787 lines;
- `cmd/silo/main.go` — approximately 3,780 lines; and
- `internal/api/handlers/playback_v3.go` — 3,393 lines.

The problem is not line count by itself. These files combine construction,
policy, storage access, protocol translation, background lifecycle, and
compatibility behavior. Setters in the router repair dependencies created
earlier in main, which makes construction order part of correctness.

Split by owned capability and make a small explicit application graph. Each
module should expose validated dependencies, start, health, and close. Route
registration should consume complete services, never finish wiring them through
post-construction setters. Large catalog/metadata modules should separate query
planning, persistence, external enrichment, and response projection.

## Medium-severity findings

### S-11 — resumable uploads are process-local

`internal/uploads/session.go` stores session metadata and chunk ownership in a
map and spool directory. Startup intentionally deletes orphaned session
directories because no durable session index exists. Plugin and diagnostics
upload handlers instantiate local managers. A retry routed to another replica
or any process restart therefore loses resumability.

Use a durable upload lease and shared/object-storage staging, or explicitly make
upload endpoints sticky and advertise that limitation. Completion must be an
atomic ownership transition; local disk can be a cache, not the session record.

### S-12 — product policy and shipped source disagree on Live TV

`AGENTS.md` states that Live TV, IPTV, tuner, EPG, and DVR functionality is a
permanently closed non-goal. The repository nevertheless contains substantial
`internal/livetv`, Jellyfin Live TV handlers, API routes, admin configuration,
and web player/UI code. Regardless of which direction is now intended, the
governance source and implementation cannot both be correct.

Make one explicit product decision. If Live TV is excluded, remove routes,
storage, UI, migrations, and compatibility claims. If it is now supported,
update the governing documentation and subject its remote-URL and distributed
session model to the same security/scale gates as playback.

### S-13 — web boundaries are too broad

The generated/manual API type surface is 5,101 lines. `VideoPlayer.tsx` is 2,871
lines, the playback session hook is 1,166 lines, and multiple admin pages exceed
1,400 lines. The player currently centralizes many real concerns, but component
state, transport ownership, capability evidence, overlays, and presentation are
still difficult to reason about independently.

Generate API types from the canonical contracts into domain modules, expose one
typed client per domain, and split the player around a playback state machine:
activation/capability generation, plan lifecycle, media adapter, controls,
overlays, and telemetry. Preserve the current runtime capability probes as the
sole browser evidence provider.

## A–Z domain matrix

| Domain | Current assessment | Primary action |
| --- | --- | --- |
| Authentication and sessions | Mature rotation/profile concepts, but browser persistence and query bearer fallbacks widen exposure | Move recovery credentials to server/cookie custody; mint scoped URL tickets |
| Authorization and tenancy | v2 authority is promising; legacy direct role checks remain pervasive | One immutable principal and typed authorization gateway |
| Catalog and metadata | Broad functionality with very large mixed-responsibility services | Split query, enrichment, persistence, and projection ownership |
| Collections | Genuine server/user models; artwork import creates authenticated SSRF | Route all remote images through the shared safe fetcher |
| Compatibility APIs | Valuable explicit boundary, but auth/playback semantics duplicate native paths | Translate once into canonical principal and playback services |
| Configuration | Typed loader exists but packages bypass it | One versioned effective-config authority |
| Downloads | Server-side artifact model is substantial | Add outbound-client ownership and continue immutable artifact checks |
| Events and notifications | Shared event hub/Redis paths exist; several local adjunct rate/batch maps remain | Classify local caches versus authoritative distributed state |
| Library scanning | Operationally rich but monolithic and env-configured in places | Bounded scan orchestration with versioned worker configuration |
| Observability | Good structured logging and metrics foundations | Add ownership generation, outbound policy, and distributed admission telemetry |
| Playback planning | Protocol v3 properly accepts client runtime evidence | Keep capability facts client-reported; make admission reservations distributed |
| Playback delivery | Restart reconstruction is thoughtfully designed | Align all limit/lease ownership with the restart-resilient model |
| Plugins | Clear process boundary, but downloader/client policy is locally constructed | Use the shared outbound service and explicit artifact trust |
| Requests and recommendations | Feature-complete large services | Decompose orchestration from repository and external-provider work |
| Storage | Postgres/S3 paths fit cluster goals; SQLite path does not | Postgres default only until real SQLite ownership/replication exists |
| Uploads | Bounded local implementation, not replica-safe | Durable upload lease plus shared staging |
| Watch state | Strong abstraction over user stores | Remove falsely durable SQLite deployment mode |
| Watch Together | Durable room rows but local live authority | Generation-fenced room owner and cross-node message relay |
| Web client | Runtime playback probing is correct; identity and file sizes need work | Preserve probe authority, remove credential URLs, split state machines |
| Workers and jobs | Many durable/advisory-lock patterns are present | Standardize lease, heartbeat, cancellation, and shutdown contracts |

## Remediation sequence

### Milestone 0 — immediate security containment

1. Disable remote collection artwork URLs or route them through the existing
   hardened image-cache fetcher.
2. Disable remote catalog-seed URLs until bounded immutable ingestion exists.
3. Replace Watch Together and realtime bearer query parameters with short-lived,
   audience-bound, single-use tickets.
4. Stop persisting administrator impersonation credentials in `localStorage`.

Exit gate: SSRF regression matrix passes; ingress logs and generated media/socket
URLs contain no general bearer or profile credential.

### Milestone 1 — distributed ownership

1. Implement atomic fleet-wide playback reservations.
2. Introduce a generation-fenced Watch Together room owner and cross-node relay.
3. Fail closed on SQLite in clustered deployments and remove the fake replicator.
4. Make resumable upload ownership durable or explicitly sticky.

Exit gate: two-replica tests prove admission cannot overbook, Watch Together
participants synchronize across replicas, and killing either owner resumes on a
survivor without violating generation or identity.

### Milestone 2 — authority consolidation

1. One outbound transport/fetch policy.
2. One immutable/versioned config authority.
3. One typed request principal and authorization gateway.
4. Repository checks banning direct production environment reads, arbitrary
   HTTP clients, and direct role checks outside named boundary packages.

### Milestone 3 — modular decomposition

1. Replace `cmd/silo/main.go` and router setter choreography with explicit
   modules and lifecycle contracts.
2. Split metadata, catalog detail, scanner, sections, collections, and playback
   v3 by responsibility.
3. Split web API types and player/admin monoliths by domain and state machine.

This milestone should be behavior-preserving. It follows the ownership fixes so
the new module boundaries reflect real authority instead of preserving current
accidental duplication.

## Verification strategy for the repairs

- Security: table-driven destination tests plus a controlled DNS server for
  rebinding and redirect cases; assert the dialed address, not only parsed URL.
- Distributed behavior: run at least two API replicas against shared Postgres,
  Redis, and S3; deliberately move clients between replicas and kill owners.
- Admission: concurrently start sessions through different replicas and assert
  the shared limit is never exceeded.
- Watch Together: attach host and guest to different replicas, exercise every
  control/readiness transition, kill the owner, and verify one successor.
- Identity: inspect reverse-proxy logs, browser history, crash telemetry, and
  WebSocket URLs for credential absence.
- Configuration: export one redacted effective snapshot with a revision and
  prove runtime components reference that revision.
- Static architecture gates: ban production `os.Getenv`, direct
  `http.DefaultClient`, and raw admin-role interpretation outside approved
  packages.

## Scope and limitations

This evaluation used source inspection and repository-local evidence. It did
not operate a production environment, inspect deployment secrets, run a
penetration test, or benchmark a cluster. Findings describe reachable code and
ownership semantics at the audited commit. The report deliberately avoids
claiming exploit success or runtime performance measurements that were not
performed.
