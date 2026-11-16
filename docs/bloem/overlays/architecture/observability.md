# Bloem overlay: Observability

This overlay covers Silo's [`docs/architecture/observability.md`](../../../architecture/observability.md), which stays exactly as Silo wrote it.

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
