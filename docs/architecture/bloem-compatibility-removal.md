# Protocol compatibility

> **Provenance.** Bloem page. It was first written for Bloem's removal of Jellyfin and
> Audiobookshelf compatibility (`b8ddec9bf`, 2026-10-13; migration renumbered in
> `933bcc5dd`) and rewritten when Bloem restored Silo's servers (`2393c41ee`,
> 2026-10-18), which is why the file name still says "removal". The compatibility code
> is Silo's; only the wiring and persistence notes below are Bloem's. Later:
> `879da820c` (2026-10-26) returned the public port and the compatibility listener
> start-up to Silo's `main.go` code. **Status:** current at `e852f92bf`; compatibility
> is served, not removed. Silo's own descriptions of the protocols are
> [Jellyfin compatibility API](../jellycompat-api.md) and
> [`internal/audiobooks/README.md`](../../internal/audiobooks/README.md).

Bloem runs upstream Silo's Jellyfin-compatible server (`internal/jellycompat`) and
Audiobookshelf-compatible server (`internal/audiobooks/abs`, `internal/audiobooks/abssocket`)
as Silo ships them. Each binds its own configured port: `jellyfin_compat.listen`
(default `:8096`) and, when `audiobookshelf_compat.enabled` is on (the default),
`:13378`. Silo's bootstrap variables `JF_PORT` and `ABS_PORT` move them (`ABS_PORT` is
Silo #3117, merged in `68070f04b`). Neither is served on the public port.

Bloem removed both on 2026-10-13 and restored them on 2026-10-18, so that Silo's code
merges without hundreds of deletion seams. Bloem's own additions from before the
removal stay removed: the companion-application enrollment, the same-origin
compatibility gateway on the public port, and Live TV through the Jellyfin surface.

## Tenant integration

Silo's packages stay upstream bytes, except one test file recorded in the seam ledger
(`contracts/seams.txt`, the reviewed list of Silo-owned files Bloem may differ in;
here `internal/jellycompat/dv_strip_test.go`, whose no-route cases turn off Bloem's
tone-mapping defaults). Bloem adapts them only where `cmd/silo/main.go` wires them,
with the adapters in `cmd/silo/bloem_compat.go` and the tenant-aware resolver in
`cmd/silo/bloem_main.go`:

- Both servers' scope resolvers are wrapped in `newTenantAwareViewerResolver`, so a
  Jellyfin or Audiobookshelf session reaches only its organization's libraries.
- Jellyfin downloads go through the native download service for the session's
  profile, with the validated tenant on the context (`newBloemCompatDownloadServer`).
  The native per-profile download policy decides; a session without a resolvable
  tenant is refused.
- Files in native storage locations are hidden from the Jellyfin handlers that need
  an OS filename (`bloemGuardNativeStorageFiles`).
- Account-wide sign-outs use Silo's path: the revoking transaction deletes the stored
  compatibility sessions, and every replica evicts its cached copies when the
  revocation is announced. A Jellyfin session also ends when its login session is
  revoked. `OnUserSessionsRevoked` returns an error so Bloem's organization lifecycle
  can report a failed invalidation.
- A Jellyfin sign-in opens an account-level login session, which Bloem's
  profile-scoped revocation (an organization removing its profiles) does not reach.
  `bloemProfileSessionRevoker` deletes those profiles' `jellycompat_sessions` rows
  and announces the revocation so replicas drop cached copies.

`GET /api/v1/health` answers `server_name` and `server_id` from the Jellyfin
compatibility configuration, as upstream does.

## Persistence

Migration `20261013161500_remove_bloem_compatibility.sql` dropped only Bloem's own
tables: application enrollment, credentials, audit and the Live TV compatibility
streams. Silo's Jellyfin and Audiobookshelf tables, the author-count view and their
settings were kept, so the restore needs no PostgreSQL migration. That migration's
Down still refuses rollback.

Bloem's earlier SQLite cleanups (shipped as user-store versions 32 and 34) dropped
`jellycompat_displayprefs` and its settings. Bloem's SQLite repair step recreates
the table; the deleted preferences do not return. See
[SQLite user-store versions](bloem-upstream-adapters.md#sqlite-user-store-versions).

## Upstream merges

Take Silo's compatibility code as it is; there are no deletion seams to preserve.
When upstream changes how `main.go` builds either server, keep the tenant wiring
above. `TestProductionViewerResolversUseBloemTenancy`
(`cmd/silo/bloem_viewer_tenancy_wiring_test.go`) fails when a production
`policy.NewViewerResolver` construction, including the compatibility servers', skips
Bloem tenancy.

Until 2026-10-26 Bloem also kept `cmd/silo` listener guard tests that allowed only
named listener functions (`newAudiobookshelfListener`, `serveAux`) to serve the
compatibility ports. `879da820c` removed `serveAux` and those tests when the public
port returned to Silo's root handler; the listeners are now Silo's own start-up code
in `cmd/silo/main.go` and `cmd/silo/abs_listener.go`.
