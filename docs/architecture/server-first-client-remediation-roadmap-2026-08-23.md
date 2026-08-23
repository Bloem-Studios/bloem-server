# Server-First Client Remediation Roadmap

**Status:** Approved for execution  
**Date:** 2026-08-23  
**Related audit:** [Server A–Z Architecture Evaluation](server-a-z-architecture-evaluation-2026-08-23.md)

## Goal

Close the server security and distributed-ownership blockers first, then finish
Watch Together, audiobooks, music, and ebooks across the required Vondel
clients against stable, explicit server contracts.

This roadmap is the durable program contract. Detailed implementation steps,
temporary test commands, and worktree-specific notes remain outside versioned
documentation.

## Architecture direction

The server is the sole authority for:

- outbound network policy;
- fleet-wide playback reservations;
- Watch Together room generations and ownership;
- scoped media and WebSocket authorization;
- cluster-safe persistence requirements.

Clients remain the authority for their real runtime playback capabilities.
They report observed device facts, consume explicit server capability
contracts, and do not substitute device-model tables or optimistic defaults for
unknown capabilities.

Postgres, Redis, and S3-compatible object storage are shared authorities.
Process memory and local disk are caches or execution state only. Every shared
mutation must be idempotent and fenced by a generation or lease.

## Program order

```text
M0  Outbound security and scoped URL tickets
                  |
          +-------+-------+
          |               |
M1  Fleet playback   M2  Distributed
    reservations         Watch Together
          |               |
          +-------+-------+
                  |
M3  Watch Together on Android phone, Android TV, iOS, and tvOS
                  |
M4  Audiobooks and music on all four clients
                  |
M5  Ebooks on Android mobile and iOS
```

Client visual work may proceed where it does not invent or freeze a server
contract. Feature routing, capability, and lifecycle work follows the order
above.

## Global invariants

- Treat a node dying mid-stream as a normal event.
- A client capability snapshot is immutable for one activation generation.
- Unknown device facts remain unknown; they are not converted to unsupported
  or replaced by handwritten declarations.
- Ordinary authenticated profiles cannot trigger requests to loopback,
  private, link-local, metadata, or DNS-rebinding destinations.
- Redirects are revalidated at every hop and the validated dial target must be
  the target actually used by the transport.
- General bearer tokens and profile credentials never appear in media or
  WebSocket URLs.
- Cluster-wide limits are enforced by shared atomic reservations, not summed
  process-local counters.
- Each Watch Together room generation has one authoritative distributed owner.
- Clustered deployments cannot start with node-local SQLite user state.
- A visible client action must have a genuine capability and a real route.
- Client-visible contract changes are coordinated across server, Android, and
  Apple repositories.

## M0: Secure outbound access and URL authorization

### Shared outbound-fetch authority

Create one server-owned outbound request policy and migrate all remote fetch
callers to it. The authority must:

- reject special-use IPv4 and IPv6 ranges, including mapped addresses;
- reject credentials embedded in URLs;
- resolve and validate every candidate address;
- prevent DNS rebinding by coupling validation to dialing;
- validate every redirect independently;
- impose response-size, timeout, and content-type limits appropriate to the
  caller;
- expose explicit policy variants only where the product contract requires
  them.

Initial consumers include image caching and webhook delivery. Collection
artwork, remote catalog imports, and future URL-taking features must use the
same authority rather than local filters.

### Collection artwork containment

Authenticated collection artwork references must never cause unrestricted
server-side fetches. Prefer server-owned artwork identifiers or scoped proxy
references. If legacy remote URLs remain temporarily supported, they must pass
through the shared outbound authority and have bounded response handling.

Acceptance requires tests for loopback, RFC1918, link-local, metadata service,
IPv4-mapped IPv6, mixed public/private DNS answers, redirect pivots, rebinding,
oversized bodies, and valid public artwork.

### Immutable, bounded remote catalog seed import

Remote seed imports must read from a validated immutable snapshot. Discovery,
validation, and final consumption cannot refer to a mutable remote resource by
URL alone. The import must have bounded compressed and expanded sizes, bounded
entry counts, safe archive paths, and a digest that is verified before commit.

### Scoped URL tickets

Replace general credentials in media and WebSocket URLs with short-lived,
audience-bound tickets. Tickets must bind the minimum required subject,
resource, action, expiry, and generation. Logs, redirects, browser history, and
receiver handoffs must not expose general bearer or profile credentials.

## M1: Fleet-wide playback admission

Introduce a shared playback reservation authority backed by Redis or an
equivalent transactional shared store. A reservation binds:

- activation and playback identity;
- profile/account policy scope;
- selected file and delivery mode;
- node assignment where applicable;
- a fencing generation and expiry.

Create, renew, replace, and release operations must be atomic and idempotent.
Abandoned reservations expire safely. Two API replicas racing at the final
available slot must admit exactly one playback. Replanning must replace or
update the existing reservation without transiently double-counting it.

This milestone is complete only after a two-replica concurrency and failover
gate passes.

## M2: Distributed Watch Together ownership

Move room generation ownership out of process memory. Each room generation has
one lease-fenced owner responsible for authoritative command ordering and room
state publication. Any API replica may accept a connection, but it must relay
commands to the current owner and reject stale generations.

The protocol must define:

- room generation creation and closure;
- owner acquisition, renewal, and fencing;
- participant join/rejoin identity;
- monotonic command sequencing;
- reconnect and replay bounds;
- failover after owner death;
- idempotent command handling;
- bounded room retention and cleanup;
- scoped WebSocket authorization.

No client-specific playback capability is promoted into the room authority.
Each participant retains its own activation-scoped playback capability snapshot
and resolves a locally valid plan.

Acceptance requires two replicas, participants connected through different
replicas, forced owner loss, generation rollover, stale-owner rejection, and
successful participant recovery.

## M2.5: Cluster-safe persistence posture

Remove any configuration that implies node-local SQLite is replicated safely.
SQLite remains an explicitly local mode protected by a PostgreSQL-backed,
durable single-node ownership claim and active-process heartbeat fence. A
second process or a different node must fail before opening local user state;
multi-replica deployments require the shared PostgreSQL backend.

Startup validation, deployment examples, and operator documentation must all
express the same rule.

## M3: Watch Together on all four clients

Required clients:

- Android phone;
- Android TV;
- iOS;
- tvOS.

Each client must implement capability-gated room creation/join, reconnection,
generation rollover, participant state, host commands, local playback
resolution, drift correction, and honest failure states. The underlying detail
or playback context remains mounted where required for exact Back/focus return.

The room coordinator remains capability-blind. Each client freezes one local
runtime capability snapshot per activation generation and uses that same
snapshot for initial planning, replanning, recovery, and Watch Together local
playback. Receiver playback uses a separately reported receiver snapshot.

This milestone starts only after the server two-replica Watch Together gate is
green.

## M4: Audiobooks and music on all four clients

### Audiobooks

Deliver dedicated audiobook playback on Android phone, Android TV, iOS, and
tvOS using genuine server metadata only. Required behavior includes chapter or
part navigation where authoritative ordering exists, bookmarks, resume,
sleep-timer lifecycle, background/remote controls where supported, error and
retry behavior, and exact route return.

Do not fabricate contributors, multipart ordering, chapters, or artwork when
the server contract does not provide them.

### Music

Deliver genuine music discovery and playback on all four clients. Required
behavior includes server-advertised music destinations, artist/album/track
routing, queue ownership, previous/next, resume or restart semantics as defined
by the product, background/remote controls, and honest unsupported states.

Music requests must use real library identifiers or supported server filters;
clients must not invent an unsupported `type=music` contract.

Audiobook and music changes are independently releasable and independently
verified.

## M5: Ebooks on Android mobile and iOS

Provide one canonical ebook route and one reader owner on each required mobile
client. Cover EPUB and PDF where supported, streamed and downloaded artifacts,
reading-position restoration, typography and theme preferences, external-link
handling, and exact Back/close behavior.

Downloaded artifacts open only after ownership, revision, positive-size, and
readability checks. Remote artifacts use scoped server delivery credentials.
Unsupported or DRM-protected formats show an honest unavailable state and do
not fall through to an audio/video player or generic external opener.

Android TV and tvOS must not advertise ebook reading unless a separate TV
reading experience is explicitly approved and implemented.

## Verification gates

Every milestone requires focused RED-to-GREEN tests and repository-specific
build gates. The final program gate requires:

- the complete SSRF and immutable-import security matrices;
- credential-free media and WebSocket URLs;
- fleet playback reservation concurrency and failover;
- Watch Together cross-replica operation and owner failover;
- clustered SQLite startup rejection;
- Watch Together, audiobook, and music journeys on all four clients;
- ebook journeys on Android mobile and iOS;
- runtime playback capability and Aether regression suites on all four clients;
- updated API, architecture, feature-changelog, and operator documentation;
- formatting, lint, local-path, and diff-hygiene checks in every changed
  repository.

Compile-only evidence is insufficient for distributed, security-sensitive, or
device-interactive behavior.

## Stop conditions

- Stop client Watch Together rollout if the two-replica server gate is not
  green.
- Stop an outbound-fetch migration if mixed public/private DNS answers or
  redirect/dial-target mismatches are accepted.
- Stop playback rollout if two replicas can overbook one shared limit.
- Stop feature rollout if a visible action lacks a genuine capability or route.
- Do not mark a milestone complete while its central behavior is supported only
  by compile-time evidence.

## Delivery and ownership

Server security and distributed-ownership changes land as independent commits
before dependent client work. Client-visible contracts are updated in the
server and both native client repositories in the same milestone. Each feature
has an evidence ledger that records accepted behavior and remaining honest
limitations without local paths or transient environment details.
