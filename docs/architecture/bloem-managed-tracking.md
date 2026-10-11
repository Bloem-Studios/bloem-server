# Native Pastime managed tracking

Bloem's `internal/managedtracking` service enrolls one source account as one Pastime account with independently tracked household profiles. It registers `bloem.managedtracking.v1.ManagedTrackingHost` through the optional installation-bound broker hook. Only the installed `bloem.pastime` plugin receives the service; every RPC checks the current installation grant and tenant authority. This is a separate protocol namespace, not a public SDK upgrade. The server remains on SDK v0.24.0 and the plugin on v0.26.0; each executable links one SDK version.

## Durable authority

The migrations add a stable instance identifier, tenant-scoped grants, credential generations, profile projections, lifecycle records, snapshots, enrollment receipts and playback authority. Profile projection filters by organization. Default identifiers survive tombstones; active defaults precede children in immutable inventory. Source writes advance committed revisions. Inventory pages contain at most 100 rows, expire after ten minutes, and use opaque issued cursors; at most sixteen snapshots coexist per installation. Only complete issued snapshots authorize lifecycle acknowledgement.

`EnsureConnection` checks exact source revisions, activity, destination IDs, generation and a stable JSON-tuple enrollment hash. It atomically commits encrypted profile-bound watch-sync credentials and replay receipts. An identical retry converges; changed content with the same enrollment ID conflicts. Disabled accounts/profiles clear tracking credentials without deleting retained history. Explicit rotation and reactivation advance generations; `GetInfo` renews generations after thirty days, ahead of the destination credential's ninety-day expiry. Revoked grants reject future requests.

## Playback provenance

Only Pastime outbound clients are wrapped. Before calling the plugin, the host records the digest of deterministic serialization of the exact published SDK event, authenticated source binding and committed source publication revision. `ResolveEvent` validates those receipts and current enrollment authority. Arrival time and caller timestamps never establish ordering. Completed history and live delivery for one viewing share a stable consumption ID, preventing duplicate plays while preserving independent profiles and genuine rewatches.

The plugin exports movie/episode start, pause and stop plus completed `MARK_WATCHED` history. Ordinary playback completion requires the host's explicit completion flag; no percentage inference is added. Reverse sync, unwatched changes, ratings, favorites and lists remain disabled.

Pastime resolves trusted external catalog identifiers before transactional effects. It rejects title-only or destination-ID shortcuts. Separate provisioning and profile tracking credentials cannot authorize browser administration or household impersonation.

## Operations and verification

Commands assume the repository root is the cwd. Build `go build ./cmd/bloem-managed-tracking`; supply existing protected `DATABASE_URL` and `SECRET_KEY` through the operator environment. The backend-only command accepts `-action grant|info|rotate|revoke -installation N`; granting also accepts `-tenant UUID`. It prints only instance, scope and generation metadata. It is not a browser setup API.

`TestRealPluginAndPastimeHouseholdAcceptance` uses separately compiled real plugin and real Pastime HTTP fixture processes, isolated PostgreSQL schemas and synthetic metadata. It covers three independent profiles, later additions/rename/deletion, completion/history overlap, stale history, cancellation/reactivation, plugin restart and grant revocation. Explicit fixture binary paths and test database URLs are required; absent fixtures skip this test. Fresh migrations and the pinned Silo mirrored-database switching check passed. The switching image was a local acceptance image, not a production release artifact.

See Pastime's `docs/managed-tracking-rollout.md` for coordinated release and rollback. Automatic enrollment remains inactive until an explicitly approved household pilot. Login ownership is separate: this host currently sends no verified issuer/subject. Email never merges accounts; interactive pending-identity claiming and user-managed exclusion remain separate product work.
