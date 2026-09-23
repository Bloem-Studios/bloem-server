# Bloem overlay: Observability

Bloem additions and overrides for the upstream Silo document [`docs/architecture/observability.md`](../../../architecture/observability.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Metrics stay on Prometheus”, after the paragraph beginning “This is deliberate and guarded: the trace-instrumentation libraries (`otelhttp`, etc.,…”. **Bloem adds:**

`GET /api/v1/admin/host-stats` is a separate, third thing: a JSON (not Prometheus-text)
endpoint reporting the **host machine's** live CPU%, memory used/total, and network
rx/tx rates, meant for a mobile admin client to render directly rather than for a
metrics backend to scrape. It's unrelated to both the OTel pipeline above and to
`/metrics`, which instruments the Go **process** (via `client_golang`'s default
collectors — CPU-seconds, resident memory, goroutines), not the host it runs on.
`internal/hoststats.Sampler` reads `/proc/stat`, `/proc/meminfo`, and `/proc/net/dev`
on a 2s background tick (never blocking the HTTP handler); `supported: false` is a
normal, always-`200` response on a non-Linux host or when `/proc` is unreadable — see
`internal/hoststats/sampler.go`'s package doc for the full contract.
