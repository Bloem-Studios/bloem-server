# Bloem overlay: Silo v1 Scope

Bloem additions and overrides for the upstream Silo document [`docs/architecture/v1-scope.md`](../../../architecture/v1-scope.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** document title. Bloem titles this document “Bloem Silo-compatible v1 Scope”.

---

**Location:** section “Silo v1 Scope”, after the paragraph beginning “This file governs v1 **capability** scope: which user-facing capabilities Silo…”. **Bloem adds:**

`/api/v1` is Bloem's Silo-compatible projection, not an unmodified upstream
surface. The route contract is pinned against `origin/main`; the reviewed
Bloem exceptions are direct profile login and lookup, account-administration
profile/device/session methods, and tenant-member lifecycle/resource methods.
Native Bloem client features belong under `/api/bloem/v1`.

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
