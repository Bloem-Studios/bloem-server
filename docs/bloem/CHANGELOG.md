# Bloem integration changelog

This records Bloem's integration milestones. Silo's image build numbers and
upstream feature history remain separate from Bloem's application commits.

## October 7, 2026

Bloem `c87b44545` is deployed with all 27 Silo commits through `74158b4a8`,
preserved through a normal merge. Native EPUB/PDF onboarding adds protected
source management, durable pathless-library recovery, binding, queueing,
authorized publication and reader delivery. Retained source/catalog identities
survive disable and uninstall; unsupported mutations refuse explicitly.

The Silo merge brings shuffle, episode download poster fields, download queue
admission, autoscan delivery/polling improvements, overlap waiting and auth/catalog
fixes. Bloem preserves canonical tenant authority and private native guards.
The declared seam count is 407; the final lint cleanup adds no seams.

Go/web builds, focused tests, real disposable-database regressions and uncapped
changed-line lint passed within their recorded scopes. Migration completion,
health/readiness, web assets and administrative refusals were checked on the
exact running binary. No database backup was taken for this explicitly approved
rollout. [Deployment evidence and limits](../operations/2026-10-07-native-storage-deployment.md)
remain distinct from production provider and native-client acceptance.

Bloem plugin SDK `v0.24.0` is published with the Silo `v0.23.0` public protocols
and Bloem storage service retained. Native OpenAPI now documents all 32 storage
administration operations, verified against their production route registration.
Generated Kotlin and Swift contracts include the native request/response shapes
and merged upstream fields. SDK versions, generation and client adoption are
tracked in the [compatibility guide](../architecture/bloem-sdk-compatibility.md).
Documentation and generated-binding updates do not by themselves redeploy the
server or enable unsupported native features.

## October 5, 2026

A larger tenant extraction subsequently restores three more complete upstream
production files, reducing declared seams from 390 to 387. Organization-qualified
group storage, extended policy fields and HTTP context adapters now live in owned
files; upstream group handlers and service interfaces remain unchanged. Four
startup helpers move to owned wiring. Across all affected shared production Go
files, the upstream diff falls from 4,930 to 4,450 changed lines, including caller
and inventory-hook costs. Existing revision semantics, DTOs and mandatory tenant
authority are preserved. This is owned host-module extraction, not optional
multi-tenant enforcement or SDK catalog packaging.
The combined cutover is deployed at `d55ebd6d` with passing health, contract,
protected-denial, branding and worker checks. See the
[merge-reduction verification record](../operations/2026-10-05-merge-reduction.md).

A subsequent merge-reduction pass restores six upstream files byte-for-byte,
reducing declared seams from 396 to 390. Trusted playback tenant facts move to
an owned checker; branding is supplied by the existing owned build adapter,
including development public assets. The codec capability declaration moves to
its owned helper. An additive Make include reduces the shared Makefile diff
from 278 to 61 changed lines while preserving its commands and verification.
Tenant isolation remains mandatory host authority. SDK catalog packaging is
still deferred; this pass measures upstream-file reduction rather than plugin
count. See the [adapter guide](../architecture/bloem-upstream-adapters.md).

Promotions and ambience evaluation move into bundled local plugin workers with
host-owned authority and storage. Shared sections use generic extension hooks;
API projections and existing records are retained without a migration. This is
local worker extraction, with catalog SDK integration deferred. The seam count
remains 396, and IPTV/Xtream extraction remains shelved. See the
[cutover verification record](../operations/2026-10-05-presentation-plugin-cutover.md).

The upstream merge imports Silo `1555e5214` (build 1116), including network
identity sign-in, subtitle synchronization, download preparation controls,
player recovery, local discovery, administrator policy defaults, and catalog
fixes. Bloem retains membership/profile policy and public compatibility behavior.
The seam ledger stays at 396 paths; deployment samples and compatibility
constants move to owned files to offset necessary integration adjustments.

The [verification record](../operations/2026-10-05-upstream-integration.md)
distinguishes passing build/contract/fixture checks from inherited full-suite
failures and native feature-adoption follow-ups.

## October 2, 2026

The normal upstream merge includes Silo `bbf12add2`: external sign-in and device
approval, link invitations, branded email, trickplay, subtitle timing, download
improvements, and dependency fixes. Bloem keeps 396 declared seams, with owned
adapters for membership-aware account transactions, current policy reads, initial
owner recovery, invitation admission, Live TV permissions, and email branding.

Generated client DTOs, settings bindings, OpenAPI, and fixtures are refreshed.
The [integration verification record](../operations/2026-10-02-upstream-integration.md)
records passing checks and the remaining full-suite limitations.

## September 30, 2026

Bloem `b2ad352a0` includes Silo upstream `8e2e84047`. The source merges preserve
upstream ancestry and retain 396 declared upstream-file seams.

### Inherited from Silo

The imported source includes account password-reset links and temporary-password
handling, age-normalized content restrictions, local and remote stream bitrate
limits, request routing and group limits, request follows and season selection,
real-time library monitoring, theme-song playback, and title-based watchlists.
These are upstream capabilities; use the relevant API's capability discovery
and the client's implemented workflows rather than assuming every client exposes
every feature.

The final follow-up adds bounded retries when a Trakt paginated listing changes
during a read: at most three attempts, restarting the inconsistent listing.
Other failures are returned without that consistency retry.

### Bloem integration

- Preserved organization membership authority and profile-aware policy through
  owned adapters, including the finalized-schema request-group migration fix.
- Retained account ownership fields during setup and shared profile readers
  during transactional lifecycle operations.
- Preserved the upstream playback/download call signatures with profile-aware
  Bloem extensions.
- Kept native deployment-readiness decoration and library relink behavior in
  the fork's integration hooks.
- Moved more web code behind lazy routes and kept Bloem's bundle allowance
  separate from upstream validation rules.

See [integration adapters](../architecture/bloem-upstream-adapters.md) for the
implementation boundary and [deployment and validation](../operations/2026-09-30-upstream-deployment.md)
for the exact running revision, migration correction, passed checks, and open
contract/scenario/UI validation work.

The later documentation refresh updates the GitHub README, contributor and
operator guides, API overlays, historical checkpoints, and links after upstream
documentation moves. A documentation commit does not replace the deployed binary.
