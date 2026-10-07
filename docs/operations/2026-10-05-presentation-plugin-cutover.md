# October 5 presentation plugin cutover verification

> Historical snapshot: preserve the dated revisions, results and proposals below.
> For current status, see the [maintained reference](2026-10-07-native-storage-deployment.md).

## Scope

Promotions and ambience evaluation move into two bundled local worker processes.
The host retains database records, tenant/profile/PIN and library authority,
administrative CRUD, dismissals, assets and existing API projections. Shared
section integration uses generic resolver, layout and projection hooks with
feature adapters in Bloem-owned files. No schema or data migration is added.

These are bundled local plugins, not catalog-installable Silo SDK releases.
The seam ledger remains 396 declared and actual paths. No whole upstream file
was retired; this step reduces feature-specific coupling rather than the seam
count. IPTV/Xtream extraction remains shelved.

## Fresh focused verification

- Promotions package database/process tests passed, including conservative
  candidate retrieval, targeting parity and delivery. A real-worker regression
  confirms that 120,000 unrelated dismissal records do not enlarge the candidate
  request beyond its limit; only current candidate dismissal IDs are sent.
- The whole ambience package passed with isolated database fixtures. Annual,
  leap-year, daylight-saving, timezone, half-open expiry and priority checks
  passed. Host validation rejects reordered/foreign output and projects UTC.
- The process bridge race suite and vet passed. Tests cover reuse, concurrent
  calls, clean environment, message caps, cancellation, timeout, crashes,
  malformed/error replies, shutdown and next-call recovery. Cancellation before
  encoding and a blocked custom encoder were reproduced and corrected.
- Actual application wiring passed with separately compiled workers and stored
  campaigns/packs in disposable child databases. Both services deliver through
  their workers and close them on application cancellation. Missing workers
  omit optional presentation while preserving CRUD; no pure-engine fallback is
  wired into the application.
- Home/section/item and native v2 projection tests passed, including opt-in,
  placement, dismissal anchors, withdrawn/suspended/restored library access and
  a non-promotion extension traversing the generic hooks. The shared response
  declaration was preserved verbatim when moved to an owned file.
- Independent reviews addressed candidate ordering, UTC projection, encoder
  cancellation and historical-dismissal minimization. Final source review had
  no open Critical, Important or Minor findings; lint-only cleanup was reviewed.

## Artifact and integration gates

Native Kotlin/Swift DTO generation, canonical digest and explicit coverage
checks passed. The generator used a diagnostic dump containing unreached
internal RPC types as its emitted digest. Both owned emitters now use the
existing reachable-graph digest; a regression confirms internal-only types do
not change it. Only generated contract digest metadata changed; revision,
generator version and wire DTOs were preserved. The canonical digest pin is
unchanged. The whole generator suite and focused emitter lint passed.

API v2 OpenAPI, web types, semantic contract checks and fixtures passed, as did
Bloem OpenAPI, route inventory, migration ledger, offline routes, seam and local
path checks. Changed-package application lint passed with zero issues, followed
by zero-issue scoped lint for both changed generator packages. No frontend
source changed; existing validated frontend assets were reused for the image.

## Limits

The inherited full Go suite failures remain documented in the upstream
integration verification record. This focused cutover does not claim that
suite is green. Swift compiler-specific generator tests were unavailable because
this host lacks swiftc; generation and golden checks passed. No native
implementation or UI acceptance run was performed.
Full authenticated protected-profile/PIN HTTP traversal and physical client
rendering remain separate acceptance work. No production-data restore was
performed or required for this development-box cutover.

See the presentation plugin architecture for protocol, authority and runtime
limits.

## Deployed image

The development instance runs source
`5e12bf7f0e1e9c42dca6a90823ee5ad71509b11b`, image
`bloem-server:main-5e12bf7f0`, based on the upstream integration checkpoint
`c06697883`. The feature commit is `85ba110f2`; the following packaging correction
lets workers inherit the existing CGO-enabled build stage. Promotions compiles
through the existing notification/mail/image utility dependency on libvips.
The build and runtime already supply its development/runtime libraries.
The complete Go 1.26 image build passed with clean source and both workers.

Transferred image layers, platform, labels and hashes of all three executables
matched. Only the server container was recreated. Environment, mounts, ports,
restart policy, device access and network mode matched the prior configuration;
Redis was not restarted. No restore rehearsal was performed.

Health became ready in 6.96 seconds and the running system information reported
`5e12bf7f`. Eleven endpoint checks passed: public discovery, branding, setup,
capabilities, OpenAPI, root HTML and compatibility discovery returned success;
two protected routes continued returning 401 without authentication. Setup
remained complete and the API v2 contract digest was unchanged. The container
had zero restarts; startup log inspection found no error or fatal markers.

Both installed workers passed real stdin/stdout protocol checks inside the
running container. Public branding launched the host-owned ambience worker,
whose process environment contained only LANG, LC_ALL and TZ. Live promotions
was checked at the installed-worker protocol level; authenticated application
delivery was verified with stored rows in disposable integration fixtures,
not a signed-in production user journey. Detailed runtime evidence is retained
in a protected deployment record rather than published with infrastructure or
account data.
