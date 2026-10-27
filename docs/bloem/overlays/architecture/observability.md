# Bloem overlay: Observability

**What this overlays.** Silo's
[`docs/architecture/observability.md`](../../../architecture/observability.md).
Bloem adds a paragraph to “Metrics stay on Prometheus” and one to “Query
attribution”. It replaces nothing. An *overlay* is a Bloem-owned page that
records Bloem's additions to, or replacements of, sections of a Silo-owned
document. The Silo file stays exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem adds two things this page must account for:

- `GET /api/v1/admin/host-stats`, a JSON host-usage endpoint for its mobile admin
  clients (`4c8e3524d`, 2026-08-26), which has read Silo's node sampler since
  `4313e6cd2` (2026-09-13);
- the `/api/bloem/v1` routes, including the read-only *preview*, whose guest
  requests skip request logging (`0ebad1b49`, 2026-10-14). The preview is a
  browse-only catalogue that guests reach on a configured preview host with
  signed guest sessions; see the
  [browse-only content policy](../../../architecture/bloem-browse-only-content-policy.md).

Bloem first documented the host-stats endpoint in the Silo file in August 2026
and moved the text here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/admin_host_stats.go`, `cmd/silo/bloem_main.go`,
`internal/api/bloem_preview_activity.go`). Silo has not changed this document
since the previous review (Silo `6de6d5e5e`).

Index: [Bloem documentation](../../README.md).

---

**Location:** section “Metrics stay on Prometheus”, after the paragraph beginning “This is deliberate and guarded: the trace-instrumentation libraries (`otelhttp`, etc.,…”. **Bloem adds:**

`GET /api/v1/admin/host-stats` is a separate, third thing: a JSON (not Prometheus-text)
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

**Location:** section “Query attribution”, after the paragraph beginning “The `api request`, `apiv2 request` and `jellycompat request` lines add, in order,…”. **Bloem adds:**

Bloem's `/api/bloem/v1` routes share the main router, so they are logged on the
`api request` line with the same `db_*` fields. Preview-guest requests (on the
configured preview host, under `/api/bloem/v1/preview/`, or carrying a guest
credential) write no `api request` line (`skipGuestLogging` in
`internal/api/bloem_preview_activity.go`), so they carry no `db_*` request fields;
their queries still count in site totals, metrics, events (with `request` false)
and summaries, and guest `/api/v2` requests keep their `apiv2 request` line.
Bloem's packages share Silo's module path, so their call sites are named like
Silo's; they add pgx call lines, so Bloem's per-site memory bound is higher than
Silo's estimate.
