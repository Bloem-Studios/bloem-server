# Bloem overlay: Silo v1 Scope

**What this overlays.** Silo's
[`docs/architecture/v1-scope.md`](../../../architecture/v1-scope.md). Bloem
retitles it, adds two passages to the opening section “Silo v1 Scope”, and keeps
its own retired items beside Silo's “Breaking removals taken before lock”
table without editing that table. An *overlay* is a Bloem-owned page that
records Bloem's additions to, or replacements of, sections of a Silo-owned
document. The Silo file stays exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem's `/api/v1` is Silo's frozen v1 surface plus a few
reviewed Bloem additions, and Bloem's own client features live under
`/api/bloem/v1`. Bloem has also retired some of its own earlier v1 additions:
the `playback.proxy_policy` setting, and two socket-ticket routes removed in
`a7436b4e5` (2026-10-25). Silo's removals table stays exactly as Silo wrote
it, and Bloem's client contract digest (`contracts/client/v1/digest.txt`, read by
`cmd/clientdtogen`) covers that table, so Bloem's retired items are recorded
here instead. Bloem first documented these differences in the Silo file in August 2026
and moved them here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/v1_silo_contract_test.go`,
`internal/api/testdata/v1_routes_with_database.txt`,
`internal/livetv/capability.go`). Since the previous review (Silo `6de6d5e5e`),
Silo changed the document three times. Two add rows to Silo's removals table,
both applying to Bloem unchanged: v1 writes of the retired AI endpoint settings
now return `400` (`c27acf246`, #3106, and `39913c51d`, #3107, 2026-10-26, when AI
providers became one list each job selects from), and `383e52922` (#3135,
2026-10-26) omits other accounts' history samples from a v1 split for a
delegated curator.

Index: [Bloem documentation](../../README.md).

---

**Location:** document title. Bloem titles this document “Bloem Silo-compatible v1 Scope”.

---

**Location:** section “Silo v1 Scope”, after the paragraph beginning “This file governs v1 **capability** scope: which user-facing capabilities Silo…”. **Bloem adds:**

`/api/v1` is Bloem's Silo-compatible projection, not an unmodified upstream
surface. Bloem adds a few reviewed v1 routes, among them direct-profile login
(`POST /api/v1/auth/profile-login`) and tenant-member administration under
`/api/v1/admin/tenants/{tenant_id}/members`. It must never drop a Silo one:
`TestV1SiloRouteContractIsNeverNarrowed` (Bloem's, since `4f5a8af62`,
2026-08-30) fails if any route in `internal/api/testdata/v1_routes_silo_contract.txt`
disappears from the router. Additions need no change to that file, and a
removal needs a deliberate deletion plus a removal entry. Native Bloem client
features belong under `/api/bloem/v1`.

---

**Location:** section “Silo v1 Scope”, after the paragraph beginning “Until lock: treat any capability not tracked as `Proposed`/`Locked` on…”. **Bloem adds:**

## Additive Bloem-native capability follow-through

Xtream live-provider consumption belongs to `/api/bloem/v1/livetv`, not the frozen
Silo v1 or v2 route surface. `CapabilityResponse` adds optional `xtream_supported`;
server-owned Kotlin/Swift bindings default an absent field to false. The dedicated
provider-creation endpoint is embedded-web/OpenAPI only; native-client provider
administration is not claimed. This additive graph change repins the client digest
without adding a removal or changing the removals table below. See
[Xtream live TV providers](../../../architecture/xtream-live-tv.md) for scope, credentials and limits.
The [September 19 deployment record](../../../operations/2026-09-19-xtream-deployment.md)
records the shipped revision and CI exception; clients still use capability discovery,
not deployment dates or revision sniffing, to decide whether support is installed.

## Reviewed Bloem-only namespace correction

The 2026-09-19 [contract adjudication](../../../architecture/bloem-contract-adjudications.md#route-decisions)
records the already-implemented relocation of 28 Prairie-derived Live TV methods
from `/api/v1/livetv/...` to `/api/bloem/v1/livetv/...`. These were Bloem additions,
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

## Retired Bloem playback proxy policy

A fork-only enum (`always` / `transcode_only` / `never`) that answered "may a pooled proxy serve these bytes" for every delivery at once. The upstream per-delivery routing settings state the same policy exactly, per delivery and per direction, so keeping both would leave two authorities over one decision. Migrated before removal: `never` becomes `api_only` on all three egress settings, `transcode_only` becomes `api_only` on direct play and remux while transcoded output keeps its `prefer_proxy` default, and `always` needs no rows because it already is the default. The migration matters because the setting existed to keep direct play off a remote transcode node with no view of the media storage; defaulting those deployments back to `prefer_proxy` would restore exactly that misconfiguration.

## Retired Bloem v1 socket ticket routes

Removed on 2026-10-25: `POST /api/v1/playback/sessions/{session_id}/control/ws-ticket`,
`POST /api/v1/watch-together/rooms/{room_id}/ws-ticket` and their request type
`watchTogetherWSTicketRequest`. Bloem had added these routes, and replaced the
handshake of Silo's v1 events, watch-together and playback-control sockets, with
its own audience tickets. The v1 sockets now run Silo's handshake:
`POST /api/v1/events/ws-ticket` is Silo's route again, which requires a profile,
and the other two sockets authenticate as Silo's do. No first-party client
called the removed routes, because the web app, bloem-apple and bloem-android
use the v2 sockets. Silo's removals table is unchanged.
