# Bloem overlay: Silo v1 Scope

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and updated on
> 2026-11-11 against `main` at `26cf9b3e4`, which includes Silo through `7bb66f9ad`. It lists what Bloem
> adds to Silo's [`docs/architecture/v1-scope.md`](../../../architecture/v1-scope.md),
> which stays exactly as Silo wrote it, including its table of removals. The
> section-by-section changes are in the [For developers](#for-developers) section.

Silo's page decides what Silo 1.0 includes and records the old features Silo
removed before 1.0. On 11 November 2026 Silo retired its older `/api/v1`: every
route there answers `410 Gone`, and Bloem's routes there went with it. Bloem now
serves Silo's `/api/v2`, its stable API, and `/api/bloem/v2`, Bloem's own API for
the features Silo does not have. This overlay records the Bloem-only features Bloem
retired.

![Bloem's three APIs side by side: Silo's retired /api/v1, which answers 410 Gone and where Bloem's earlier additions went with it; Silo's stable /api/v2; and Bloem's own /api/bloem/v2 for organizations, plans, Live TV and more. Below, who calls what: the web app and the storefront call /api/v2 and /api/bloem/v2 only, the phone apps' old /api/v1 calls answer 410, and Silo's own apps use Silo's APIs](../../../architecture/images/overlay-v1-scope/three-apis.svg)

## What Bloem changes

- **`/api/v1` is Silo's tombstone.** Since Silo retired it, Bloem mounts nothing
  there; the routes Bloem had added, such as a profile's own sign-in, answer `410`
  like the rest. A test fails if any other `/api/v1` route appears.
- **New Bloem features go under `/api/bloem/v2`.** The web app and the storefront
  stopped calling `/api/v1` on November 1, 2026.
- **Bloem's retired features are listed here, not in Silo's table.** Silo's table
  of removals stays exactly as Silo wrote it.

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| The `/api/v1` tombstone | `internal/api/testdata/v1_routes_*.txt`, checked by `TestV1RouteSurfaceIsUnchanged` |
| Bloem's own API | routes under `/api/bloem/v2` |
| The apps' contract fingerprint | `contracts/client/v1/digest.txt`, read by `cmd/clientdtogen` |

## For developers

### Where this overlay came from

Until 2026-11-11 Bloem's `/api/v1` was Silo's frozen v1 surface plus a few reviewed
Bloem additions; since Silo retired it, it is Silo's tombstone alone, and Bloem's own
client features live under `/api/bloem/v2`. Before that, Bloem had already retired some
of its own earlier v1 additions: the `playback.proxy_policy` setting,
and two socket-ticket routes removed in `a7436b4e5` (2026-10-25). Silo's removals
table stays exactly as Silo wrote it, and Bloem's client contract digest
(`contracts/client/v1/digest.txt`, read by `cmd/clientdtogen`) covers that table,
so Bloem's retired items are recorded here instead. Bloem first documented these
differences in the Silo file in August 2026 and moved them here in `47ea7c889`
(2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/v1_silo_contract_test.go`,
`internal/api/testdata/v1_routes_with_database.txt`,
`internal/livetv/capability.go`). Silo had then added two rows to its removals
table, both applying to Bloem unchanged: v1 writes of the retired AI endpoint
settings return `400` (`c27acf246`, #3106, and `39913c51d`, #3107, 2026-10-26,
when AI providers became one list each job selects from), and `383e52922` (#3135,
2026-10-26) omits other accounts' history samples from a v1 split for a delegated
curator. Silo has not changed the document since, through `63ef34b23`. This
rewrite added the note on leaving Silo's `/api/v1`, and the update of 2026-11-11 the
retirement itself.

### Section by section

---

**Location:** document title. Bloem titles this document “Bloem Silo-compatible v1 Scope”.

---

**Location:** section “Silo v1 Scope”, after the paragraph beginning “This file
governs v1 **capability** scope: which user-facing capabilities Silo…”. **Bloem adds:**

Silo retired `/api/v1` (Silo #3200, merged into Bloem in `26cf9b3e4`): the main
listener answers every route there with `410 Gone` and `client_upgrade_required`,
except the health and readiness probes. The routes Bloem had added there, among them
direct-profile login (`POST /api/v1/auth/profile-login`) and tenant-member
administration under `/api/v1/admin/tenants/{tenant_id}/members`, went with it; the
tenant routes live on under `/api/bloem/v2/admin/platform/tenants`, and direct-profile
login has no replacement. `TestV1RouteSurfaceIsUnchanged` holds `/api/v1` to exactly
Silo's tombstone.

Bloem adds nothing to Silo's `/api/v1`. A Bloem feature gets a typed
route under `/api/bloem/v2` with an OpenAPI declaration; where Silo's `/api/v2`
lacks something Bloem needs, Bloem adds a native route rather than calling
`/api/v1`. The storefront moved off `/api/v1` on 2026-10-31 and the web app on
2026-11-01; its signed-out seasonal layer, for example, reads
`GET /api/bloem/v2/ambience/public` instead of the `ambience` block of
`/api/v1/theme/branding`. See
[leaving Silo's `/api/v1`](../../../architecture/bloem-leaving-silo-v1.md).

---

**Location:** section “Silo v1 Scope”, after the paragraph beginning “Until lock:
treat any capability not tracked as `Proposed`/`Locked` on…”. **Bloem adds:**

#### Additive Bloem-native capability follow-through

Xtream live-provider consumption belongs to `/api/bloem/v2/livetv`, not the frozen
Silo v1 or v2 route surface. `CapabilityResponse` adds optional `xtream_supported`;
server-owned Kotlin/Swift bindings default an absent field to false. The dedicated
provider-creation endpoint is embedded-web/OpenAPI only; native-client provider
administration is not claimed. This additive graph change repins the client digest
without adding a removal or changing the removals table below. See
[Xtream live TV providers](../../../architecture/xtream-live-tv.md) for scope, credentials and limits.
The [September 19 deployment record](../../../operations/2026-09-19-xtream-deployment.md)
records the shipped revision and CI exception; clients still use capability discovery,
not deployment dates or revision sniffing, to decide whether support is installed.

#### Reviewed Bloem-only namespace correction

The 2026-09-19 [contract adjudication](../../../architecture/bloem-contract-adjudications.md#route-decisions)
records the already-implemented relocation of 28 Prairie-derived Live TV methods
from `/api/v1/livetv/...` to `/api/bloem/v2/livetv/...`. These were Bloem additions,
not an upstream Silo capability. Older fork callers must use the native prefix;
compatibility aliases and redirects are not provided. The route guard preserves
the historical snapshots and checks each exact native replacement rather than
ignoring the subtree. It separately requires three existing upstream OAuth routes
missing from the older surface snapshot. This records a fork namespace correction,
not another runtime removal or a change to Silo's historical removals table.

---

**Location:** section “Breaking removals taken before lock”. The retired
Bloem-only `playback.proxy_policy` explanation is kept in this overlay.
The upstream removals table is preserved verbatim; the earlier claim that it
still contains a Bloem row is obsolete. Contract digest reconciliation after
that move is tracked in the
[September 30 validation record](../../../operations/2026-09-30-upstream-deployment.md#outstanding-validation).

#### Retired Bloem playback proxy policy

A fork-only enum (`always` / `transcode_only` / `never`) that answered "may a pooled proxy serve these bytes" for every delivery at once. The upstream per-delivery routing settings state the same policy exactly, per delivery and per direction, so keeping both would leave two authorities over one decision. Migrated before removal: `never` becomes `api_only` on all three egress settings, `transcode_only` becomes `api_only` on direct play and remux while transcoded output keeps its `prefer_proxy` default, and `always` needs no rows because it already is the default. The migration matters because the setting existed to keep direct play off a remote transcode node with no view of the media storage; defaulting those deployments back to `prefer_proxy` would restore exactly that misconfiguration.

#### Retired Bloem v1 socket ticket routes

Removed on 2026-10-25: `POST /api/v1/playback/sessions/{session_id}/control/ws-ticket`,
`POST /api/v1/watch-together/rooms/{room_id}/ws-ticket` and their request type
`watchTogetherWSTicketRequest`. Bloem had added these routes, and replaced the
handshake of Silo's v1 events, watch-together and playback-control sockets, with
its own audience tickets. The v1 sockets then ran Silo's handshake again, with
Silo's `POST /api/v1/events/ws-ticket`, until they went with Silo's v1 API on
2026-11-11. No first-party client
called the removed routes, because the web app, bloem-apple and bloem-android
use the v2 sockets. Silo's removals table is unchanged.

### Related pages

- [Leaving Silo's `/api/v1`](../../../architecture/bloem-leaving-silo-v1.md): what
  moved, and what went with the retirement.
- [API contract overlay](api-contract.md): Bloem's own API and Silo's `/api/v1`
  retirement.
- [Realtime API overlay](../realtime-api.md): sockets and tickets.
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_v1_scope.py` into
`docs/architecture/images/overlay-v1-scope/`. Redraw it there, not by hand.
