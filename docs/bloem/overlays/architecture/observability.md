# Bloem overlay: Observability

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`, and checked again on
> 2026-11-11, when the server-load reading's route was retired with Silo's v1 API. It
> lists what Bloem
> adds to Silo's
> [`docs/architecture/observability.md`](../../../architecture/observability.md),
> which stays exactly as Silo wrote it. Bloem replaces nothing there. The additions
> are in the [For developers](#for-developers) section.

Silo's page explains how the server reports on itself: logs, metrics, traces and
which database queries each request made. Bloem reports the same way. It adds one
note: the visitors of the storefront preview, whose requests are not written to the
request log. A small server-load reading for administrator apps lived on Silo's v1 API
and was retired with it.

## What Bloem changes

- **A server-load reading for apps (retired).** Bloem's administrator apps read the
  server's live CPU, memory and network use from one simple route on Silo's v1 API. That
  route went when Silo retired the API on 2026-11-11; Silo's
  `GET /api/v2/admin/system/resources` reports the same measurements.
- **Preview visitors are not logged per request.** People looking around through
  the storefront's **preview** (a browse-only guest visit) do not get a line in the
  request log. Their database work still counts in the totals and metrics.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Server-load reading (retired) | the former v1 `GET /admin/host-stats` (`internal/api/handlers/admin_host_stats.go`, no longer mounted) |
| The measurements | Silo's `internal/nodemetrics` sampler |
| Preview visitors | preview guest requests (`internal/api/bloem_preview_activity.go`) |
| Not logged | `skipGuestLogging` |

## For developers

### Where this overlay came from

Bloem added `GET /admin/host-stats` on Silo's v1 API, a JSON host-usage endpoint for
its mobile admin clients, in `4c8e3524d` (2026-08-26); it has read Silo's node sampler
since `4313e6cd2` (2026-09-13). It went with Silo's v1 API on 2026-11-11; its handler
is no longer mounted, and Silo's `GET /api/v2/admin/system/resources` is the replacement. The `/api/bloem/v2` routes include the read-only
preview, whose guest requests skip request logging since `0ebad1b49` (2026-10-14).
The preview is a browse-only catalogue that guests reach on a configured preview
host with signed guest sessions; see the
[browse-only content policy](../../../architecture/bloem-browse-only-content-policy.md).
Bloem first documented the host-stats endpoint in the Silo file in August 2026
and moved the text here in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/admin_host_stats.go`, `cmd/silo/bloem_main.go`,
`internal/api/bloem_preview_activity.go`). From `3e2d306b2` through `63ef34b23`
Silo changed the document four times: the all-in-one Compose deployment (#3180),
keeping two Compose files (#3279), remote viewers on lossy, long links (#3280) and
bounded retained identity display and codec bytes in telemetry (#2773). All apply
to Bloem as written, and both passages this overlay points at are unchanged.

### Section by section

---

**Location:** section “Metrics stay on Prometheus”, after the paragraph beginning
“This is deliberate and guarded: the trace-instrumentation libraries (`otelhttp`,
etc.,…”. **Bloem adds:**

Until 2026-11-11, the v1 `GET /admin/host-stats` was a separate, third thing: a JSON (not Prometheus-text)
endpoint reporting live CPU%, memory used/total, and network rx/tx rates, meant for a
mobile admin client to render directly rather than for a metrics backend to scrape. It's
unrelated to the OTel pipeline above. It reads Silo's `internal/nodemetrics` sampler, so
its figures follow [Node resource sampling](../../../architecture/observability.md#node-resource-sampling),
including the cgroup and lxcfs corrections; `GET /api/v2/admin/system/resources` reports
the full sample. Bloem runs its own sampler instance on a 2s tick (`cmd/silo/bloem_main.go`),
beside the API host's five-second one, and the handler only reads its latest snapshot,
converting MB and bits per second back to the bytes and bytes per second it has always
returned. `supported: false` is a normal, always-`200` response on a non-Linux host,
before the first sample, or when no sampler is wired — see
`internal/api/handlers/admin_host_stats.go`.

---

**Location:** section “Query attribution”, after the paragraph beginning “The
`api request`, `apiv2 request` and `jellycompat request` lines add, in order,…”.
**Bloem adds:**

Bloem's `/api/bloem/v2` routes share the main router, so they are logged on the
`api request` line with the same `db_*` fields. Preview-guest requests (on the
configured preview host, under `/api/bloem/v2/preview/`, or carrying a guest
credential) write no `api request` line (`skipGuestLogging` in
`internal/api/bloem_preview_activity.go`), so they carry no `db_*` request fields;
their queries still count in site totals, metrics, events (with `request` false)
and summaries, and guest `/api/v2` requests keep their `apiv2 request` line.
Bloem's packages share Silo's module path, so their call sites are named like
Silo's; they add pgx call lines, so Bloem's per-site memory bound is higher than
Silo's estimate.

### Related pages

- [Browse-only content policy](../../../architecture/bloem-browse-only-content-policy.md):
  the preview and its guest sessions.
- [How plans work](../../../architecture/bloem-plans.md): the storefront preview in
  plain language.
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
