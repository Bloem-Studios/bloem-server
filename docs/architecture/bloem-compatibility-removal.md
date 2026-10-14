# Protocol compatibility removal

Bloem serves its native APIs and frontend. Jellyfin and Audiobookshelf protocol
packages, gateways, auxiliary listeners, application enrollment and UI choices are
removed. Native library, audiobook presentation/playback, collections, progress,
OIDC/device/profile authentication and Live TV remain supported. The storefront
product and Authentik application remain Silo; their names are outside this change.

## Persistence boundary

Migration `20261013161500_remove_bloem_compatibility.sql` drops Bloem's own
compatibility persistence: the application enrollment, credential and audit
tables and the Live TV compatibility streams. The Jellyfin and Audiobookshelf
tables, the ABS author-count materialized view and the `jellyfin_compat.`,
`audiobookshelf_compat.`, `audiobooks.abs.` and `jellycompat:` settings belong to
upstream Silo, which can serve the database again while the membership policy is
mirrored. They stay until Silo switching ends
(`docs/architecture/silo-backend-switching.md`). Bloem writes none of them and reads
one: `jellyfin_compat.server_id`, which `GET /api/v1/health` still answers as
`server_id`, so clients that enrolled against health before the removal keep the
server ID they stored. A database without that row answers the deployment identity
(`server.identity_id`), and `server_name` is the branding name
(`internal/api/handlers/bloem_health_identity.go`). Native canonical collections
and watch progress stay intact.
Historical SQL remains unchanged; the displayprefs parser lives in
`internal/legacymigrate/displayprefs` solely to execute old migration chains.
SQLite schema version 32 removes legacy displayprefs storage and keys.

The PostgreSQL cleanup is transactional and uses no `CASCADE`. An unexpected
schema dependency fails the whole migration. Its Down explicitly refuses rollback:
erased credentials and bookmarks cannot be reconstructed. Restoration requires the
pre-removal database backup and matching old binary. This implementation does not
establish that browse-only accounts are safe; native content authorization and the
preview principal require separate work.

## Upstream merges

Run `scripts/verify-no-compat.sh` and its fixture test after every upstream merge.
The gate checks explicit retired package/page paths, imports and registrations;
it does not reject historical SQL or generic uses of the word compatibility.
`contracts/seams.txt` declares every upstream-owned deletion individually. The
user-directed deletion is the reviewed exception to the normal shrinking seam
budget and incurs ongoing merge-conflict cost.

For delete/modify conflicts, prune only paths explicitly declared deleted in the
ledger. Resolve shared files manually, preserving native changes from upstream;
never apply wildcard deletion to unrelated code. Regenerate OpenAPI, settings,
route inventory and client DTOs, then verify absence, seams, native authentication,
media/audiobook playback, migrations and Live TV before merging.

## Evidence

Removal acceptance covers fresh PostgreSQL installs, seeded upgrades preserving
native collections/progress, repeated startup and atomic failure on unknown
schema dependencies. SQLite acceptance covers fresh stores and reopened version-31
stores. HTTP tests drive the production router/root composition with GET, HEAD,
POST, Range and credential fixtures. Former protocol paths may return the frontend
shell; a shell response is not protocol support. Removed API paths return 404.

Historical origin metadata in native playback observations and recipe records is
retained for stored-record/client-schema compatibility; it does not mount a
protocol handler. Native node recipe/delivery stores and Jellyfin history imports
have independent native callers and are retained. Compatibility-only session
activity/expiry adapters and the Socket.IO dependencies are removed.

The six retired v1 admin/connect methods are individually adjudicated in
`bloem_v1_adjudication_test.go`. Frozen route baselines remain unchanged;
no prefix-wide exemption hides unrelated native route drift.
