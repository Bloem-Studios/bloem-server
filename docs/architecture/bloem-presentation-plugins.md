# Bloem presentation plugins

Promotions and seasonal ambience use separately runnable local evaluator plugins.
The server retains their existing database records, administrative CRUD, native
API projections, and access authority. No campaign or pack migration is required.

## Host authority

The host loads only conservatively filtered, organization-authorized candidates
and captures a single evaluation instant. It supplies minimal presentation facts,
not database URLs, signing keys, encrypted settings or stored author metadata.
Membership, profile/PIN, library access, dismissal state and asset access remain
host-owned. Returned IDs must belong to the request's authorized snapshot;
returned content and assets are checked and rebuilt from that snapshot.

Promotions evaluates candidate selection and delivery separately, preserving
lazy role/membership reads and the distinction between unrestricted libraries
and an explicitly empty allowed-library list. Ambience computes active yearly
occurrences using its embedded timezone data. Host validation constrains those
occurrences without invoking a fallback seasonal selector.

## Runtime

The reviewed bundled executables are:

- `/usr/local/lib/bloem/plugins/promotions`
- `/usr/local/lib/bloem/plugins/ambience`

The generic process bridge is in `internal/bloempresentation`. The worker stream
uses version-one newline-delimited JSON request/response envelopes. Calls are
serialized, messages are capped at 4 MiB, and queued/active exchanges have a
750 ms budget bounded by the caller's earlier deadline. The bridge reuses a
worker lazily; failures, malformed envelopes, crashes or active cancellation
dispose of it, and the next call starts a new worker. Application shutdown
closes the clients.

The budget covers queue admission, request encoding and worker I/O. Go cannot
preempt arbitrary JSON callbacks: an abandoned encoder retains the client gate
until it finishes, and its input must remain immutable until then. Process
startup/reaping and custom response decoders are synchronous. Current bundled
workers use bounded plain response DTOs; this bridge is not an executor for
unreviewed plugins or arbitrary callback code.

Only locale/timezone variables are supplied to workers. The application's
credentials, proxy environment, HOME and PATH are not inherited. This is
credential separation, not an operating-system sandbox: reviewed executables
retain the process user's filesystem/network privileges. They perform pure
selection and projection and do not spawn descendants or fetch assets.

Optional plugin failure omits promotions/ambience instead of blocking browsing,
login or playback. Caller cancellation is propagated. Runtime wiring explicitly
uses process evaluators, including when an executable is missing; it never
silently falls back to the pure compatibility/test constructors.

## Build and scope

Docker builds both worker commands and bundles them with the host. Workers
inherit the CGO-enabled build stage and its existing libvips dependencies; the
runtime image supplies the corresponding libraries. For local
work, `scripts/bloem-build-presentation-plugins.sh` emits the same two executable
names to an optional output directory. The application supports a reviewed
absolute `BLOEM_PRESENTATION_PLUGIN_DIR` directory override for development.

These are bundled local plugins, not catalog-installable Silo SDK releases.
Catalog lifecycle integration is a separate step. This cutover avoids adding a
new SDK capability or granting plugins raw database access merely to move
optional presentation logic. IPTV/Xtream work remains shelved; existing Live TV
functionality is unaffected by this extraction.

The shared section integration uses reusable extension hooks with feature
adapters and projections in owned files. Core tenancy, profiles and progress
remain core authority. The seam ledger measures actual upstream-file differences;
moving a service alone is not evidence that a shared seam disappeared.
