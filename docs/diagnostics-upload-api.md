# Diagnostics upload API

The native v2 diagnostics ingress uses the existing diagnostics validator and
storage service. The bridge endpoints retain their wire contract.

`GET /api/v2/diagnostics/capabilities` requires a signed-in account access token;
API keys are refused. Revision `1` returns the existing account-specific status,
server instance identity, accepted manifest schema versions, bundle and manifest
limits, retention days and consent notice version. Capability state is `available`,
`disabled`, or `not_configured` according to diagnostics availability. A missing
service returns a dependency-unavailable problem.

`upload_chunk_bytes` is zero. This v2 namespace currently exposes only single
multipart uploads; clients must not infer v2 chunk support from bridge support.

`POST /api/v2/diagnostics/reports` requires the same account access token and is
refused in demo mode. Send `multipart/form-data` with exactly two file parts in
this order:

1. `manifest`, `application/json`, at most 65,536 bytes.
2. `bundle`, `application/gzip`, bounded by the advertised `max_bundle_bytes`.

The optional `X-Profile-Id` attributes the captured report. It must match the
manifest and belong to the authenticated account; child profiles are forbidden.
It is independent of the current viewer and does not require an active viewer
PIN. The existing service validates attribution, schema, destination, consent,
archive metadata and quota before accepting a report.

Ingress streams the gzip part through the existing validator without buffering
the complete multipart body in Huma. It applies the live bundle limit plus the
existing 128 KiB framing allowance, finite ten-minute upload deadlines and the
same process-local admission limiter used by bridge uploads and chunk completion.
The OpenAPI multipart schema is attached after Huma input registration so Huma's
compiled decoder does not pre-read the stream; the media gate still enforces the
declared request content type.

Success is `201` with `report_id` and `short_id`. Failures use native Problem
Details with the existing validation messages and corresponding HTTP statuses.
Explicit quota and busy rejections include `Retry-After`. Uploads are
`non_retryable`: an uncertain response may follow a completed report, so clients
must not replay automatically. `Retry-After` does not make an uncertain upload
safe to repeat.

The retained bridge chunk sessions use process memory and temporary spool files,
with a fifteen-minute lifetime, sixteen-session cap and one session per account.
They require affinity to the creating process. Another replica or a process
restart may return `404`, requiring a fresh session; this is not durable
cross-replica resumability. This v2 ingress does not change those constraints.

Apple and Android upload transports and the web diagnostics-status consumer must
adopt the v2 discovery and ingress together before migration ratification. The
Jellyfin compatibility surface has no corresponding diagnostics upload contract.
