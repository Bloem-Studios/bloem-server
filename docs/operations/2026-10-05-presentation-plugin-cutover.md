# October 5 presentation plugin cutover verification

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
limits. Deployment evidence is recorded separately after the cutover image starts.
