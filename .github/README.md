<p align="center">
  <img src="../assets/bloem-banner.png" alt="Bloem Server" width="520" />
</p>

# Bloem Server

> [!NOTE]
> This is Bloem's front page. It lives at `.github/README.md`, which GitHub shows
> in place of the root `README.md`. The root `README.md` is Silo's own and stays
> exactly as Silo wrote it, so that Silo's changes keep merging cleanly; read it
> for Silo's description of the software. The same rule applies throughout:
> files that also exist in Silo are Silo's, and Bloem's documentation lives in
> Bloem-owned files indexed in [docs/bloem/README.md](../docs/bloem/README.md).

Bloem Server is a public **tracking fork** of
[Silo Server](https://github.com/Silo-Server/silo-server), maintained by
Bloem Studios: it keeps Silo's full Git history and merges Silo's `main` as it
moves, rather than copying Silo once and diverging. Most of the code, including
the scanner, catalog, playback pipeline, web app and plugin runtime, is Silo's.
Bloem adds organizations and multi-tenant access policy, Live TV and DVR,
storage-plugin libraries, campaign and seasonal presentation, and native APIs for
Bloem's own clients. It keeps upstream module paths, protocol identifiers and
environment-variable names (`SILO_DATA_ROOT`, `X-Silo-*`, `cmd/silo`) so
upstream commits remain mergeable and existing Silo-compatible clients keep
working against it. The distributed web application and visual assets identify
the product as Bloem, as [TRADEMARK.md](../TRADEMARK.md) requires of forks.
[FORK.md](../FORK.md) covers provenance, deliberate divergence and the
protected-upstream remote setup.

> [!IMPORTANT]
> **Run Bloem on your Silo database, and switch back whenever you like.**
> Bloem can take over an existing Silo installation in place and hand it back
> again: both servers take turns on the same database, and accounts, profiles,
> permissions, roles, watch history and settings written by one are there when
> the other starts. One server runs at a time; a switch is a stop, a start and a
> route change. Bloem-only features (more organizations, profile logins, Live TV)
> wait unused while Silo serves. Keep Silo on a release Bloem has already merged:
> a newer one can change the schema in ways Bloem has not adapted to, and an
> older one may not read a schema a later Silo migration changed.
> See [Silo backend switching](../docs/architecture/silo-backend-switching.md)
> and the operator steps in the
> [Docker guide](../docs/bloem/overlays/wiki/deployment/docker.md#switching-between-silo-and-bloem).

## Current state

Checked on October 27, 2026, against `main` at `e852f92bf`.

| | |
| --- | --- |
| Bloem `main` | `e852f92bf`, a documentation commit; the last code change is `276becf4a` |
| Silo integrated | all of Silo `main` through `3e2d306b2` (October 27, 2026, 03:06 UTC), merged in `68070f04b`; `main` is zero commits behind Silo |
| Seam ledger | 306 Silo-owned files that Bloem differs in, declared in [`contracts/seams.txt`](../contracts/seams.txt) |
| Production | `276becf4a` since 07:55 UTC on October 27, from the image Bloem CI built and published; see the [October 27 receipt](../docs/operations/2026-10-27-main-deployment.md) |

The latest Silo merges brought mostly security hardening. Tokens are bound to
their session's account, including on Bloem's preview, plugin, tenant-admin and
native-video paths. v1 progress and stop, and Bloem's durable stop, refuse
another profile's session. Signed stream and download tokens stay out of logs,
and account-level downloads check profile access. The plugin cache refuses a
directory another local user controls, and many inputs are bounded. The same
merges added AI providers that are connected once and chosen per job, the
embedding provider chosen from AI Services, playback that starts on an audio
track the client plays without conversion, and file names that are not valid
UTF-8. Bloem's own changes on October 26 moved account policy back onto Silo's
`users` columns and removed music, lifecycle receipts and the presentation worker
processes (see [What Bloem removed](#what-bloem-removed-and-why)). The
[integration changelog](../docs/bloem/CHANGELOG.md) records each step with its
commit.

What this does and does not establish:

- **A source integration is not a deployment.** `main` containing Silo's
  commits says nothing about what a server runs; the dated receipts under
  [`docs/operations/`](../docs/bloem/README.md#deployment-receipts) do.
- **A deployment is not client or playback acceptance.** The October 27 receipt
  records health, migrations, logs and active streams after the restart, not a
  test of every client or provider. Bloem's own native clients are unreleased;
  check the [client contract transition](../docs/architecture/bloem-contract-adjudications.md)
  before distributing one, especially for nullable profile limits.
- **Public preview** was installed but disabled in the
  [October 15 receipt](../docs/operations/2026-10-15-preview-server-deployment.md);
  no later receipt records enabling it.

Earlier checkpoints, including the October 7 deployment of `c87b44545` with Silo
through `74158b4a8` and 407 seams, are historical; the
[documentation index](../docs/bloem/README.md#deployment-receipts) lists every
dated receipt.

## Where this code comes from

### Silo

[Silo](https://github.com/Silo-Server/silo-server) is a self-hosted media server
for films, series, audiobooks, ebooks, podcasts and manga, licensed
AGPL-3.0-or-later, with its trademarks held by Silo Media L.L.C. It is pre-release
and changes daily: its 1.0 scope is movies and series, with books and
Audiobookshelf compatibility labelled beta. Silo ships the web app, the
`/api/v2` native API, a frozen alpha `/api/v1`, Jellyfin- and
Audiobookshelf-compatible servers on their own ports, plugin-driven metadata and
distributed proxy and transcode roles. Its user manual is
[siloserver.org](https://siloserver.org/docs).

### Bloem

Bloem is Bloem Studios' media platform, and this repository is its server. It
serves several organizations from one installation, each with its own members,
profiles, libraries and entitlements, and it adds Live TV, storage-plugin
libraries and presentation features (announcements, promotions, seasonal
ambience) that Silo does not have. Bloem's own clients, `bloem-apple` and
`bloem-android`, are clean-room projects built from documented server
interfaces, not forks of Silo's apps, and are unreleased. The web app in this
repository is Silo's, with Bloem's additions and build-time branding.

### Why a tracking fork

The fork began on August 13, 2026 (`a51326741`), under the working name Vondel
Server, renamed Bloem on August 24. An earlier attempt had imported Silo as a
single new root commit with the Go module path renamed. That rename touched
1,178 files, which would have made every later Silo change a conflict, and the
import shared no history with Silo, so Git could not merge Silo at all.
`a51326741` replaced it with Silo's real history. Bloem's additions are mostly
new packages, migrations and routes, which merge cleanly without a rename, so
Bloem keeps Silo's module path (`github.com/Silo-Server/silo-server`) and takes
Silo's changes as ordinary merges. See [FORK.md](../FORK.md#this-is-a-tracking-fork-deliberately).

### How Silo's changes reach Bloem

Two Bloem-owned workflows automate the merge:

1. [`upstream-sync.yml`](workflows/upstream-sync.yml) runs daily (scheduled for
   05:20 UTC) and on demand, with a dry-run option. It fetches Silo's `main`; if
   Bloem is behind, it merges it with `git merge --no-ff` on a branch named
   `upstream-sync/<date>-<sha>`.
   - On a clean merge it runs `go build ./...` as a quick signal (a failure is
     reported, not fatal) and opens a pull request.
   - On a conflict it commits the conflicted tree, markers included, and opens a
     **draft** pull request listing the conflicting files, so a person resolves
     it.
   - It never force-pushes and never writes `main`. It pushes with the
     `UPSTREAM_SYNC_TOKEN` repository secret, because events raised by GitHub's
     default token start no CI and that token cannot push Silo's workflow-file
     changes. It reports the outcome to Discord when a webhook is configured.
2. [`upstream-sync-merge.yml`](workflows/upstream-sync-merge.yml) runs when Bloem
   CI finishes on an `upstream-sync/` pull request. It loads its merge helper
   (`scripts/verify-upstream-sync-merge.sh`) from the default branch, never from
   the pull request. It merges with a merge commit, never a squash, and only
   when:
   - CI succeeded;
   - the pull request is not a draft;
   - its head is the exact commit CI tested;
   - that head is the untouched, conflict-free merge of a `main` commit with a
     Silo commit.

   A resolved conflict, an amendment or an extra commit means a person merges
   it.

Maintainers also merge Silo by hand when Bloem's code needs adapting in the same
merge; the two most recent integrations, `303511a25` and `68070f04b`, were such
merges. Either way, Bloem CI gates `main`. See [Bloem CI](../docs/bloem/ci.md#upstream-sync-merges).

### "Silo wins"

In shared code, Bloem takes Silo's side of every conflict it can adapt around,
then reapplies the smallest Bloem delta in Bloem-owned files. Every Silo line
Bloem keeps edited conflicts again on the next merge, and Silo moves daily, so a
small footprint is what keeps the fork mergeable. Taking Silo's implementation
also brings Silo's fixes and tests. Bloem's native clients being unreleased is
not a reason to keep a Bloem difference: Silo's wire changes are taken as
shipped and the generated client contracts are regenerated. Where Bloem keeps a
swap after review (for example the outbound address guard, which refuses IPv6
special-use ranges Silo's does not), the [changelog](../docs/bloem/CHANGELOG.md)
records why. The rule is set out in [BLOEM_AGENTS.md](../BLOEM_AGENTS.md#shared-implementation-follows-silo).

### Seams and overlays

A **seam** is a Silo-owned file, one that also exists in Silo's `main`, that
Bloem changes. Each one is listed, usually with its reason, in
[`contracts/seams.txt`](../contracts/seams.txt). `make verify-seams` fails if
Bloem differs from Silo in any file the ledger does not list, and CI runs it. A
new entry is a reviewed decision; the count may only go down. It stands at 306.
Many seams are a single hook line or a generated artifact that has to differ
because Bloem registers more routes. Older pages quote larger counts, such as
407 on October 7 or 852 while Bloem had removed Jellyfin compatibility; those
are history.

An **overlay** is the documentation counterpart. Silo's Markdown files stay as
Silo wrote them, and a Bloem-owned file under
[`docs/bloem/overlays/`](../docs/bloem/overlays/), at the same relative path,
records section by section what Bloem adds or replaces. The
[documentation index](../docs/bloem/README.md#overlays-on-upstream-documents)
lists them.

### Live TV from Prairie Server

Live TV, OTA/DVR and EPG support is a separate attributed AGPL adaptation of the
[Prairie Server](https://github.com/Prairie-Server/prairie-server) subsystem;
[`docs/livetv/prairie-source-manifest.tsv`](../docs/livetv/prairie-source-manifest.tsv)
pins the source it was adapted from. Xtream live providers are Bloem's own
addition to it.

## Credit where it's due

Bloem Server exists because [Silo Server](https://github.com/Silo-Server/silo-server)
does. The scanner, catalog, playback pipeline, plugin runtime, and the large
majority of this codebase's day-to-day behavior are Silo's own design and
implementation, licensed AGPL-3.0-or-later, carried forward here with its
license and copyright notices intact — see [LICENSE](../LICENSE) and
[FORK.md](../FORK.md). If you find this project useful, the credit — and any
support you'd like to give — belongs first to the people building Silo
itself: <https://github.com/sponsors/quick104>.

## What Bloem adds on top of Silo

The overview below describes Bloem's additions at `e852f92bf`, alongside Silo
through `3e2d306b2`. The
[web coverage matrix](../docs/architecture/bloem-web-feature-coverage.md) separates
implemented workflows from bounded acceptance evidence. Upstream features remain
credited to Silo; a server endpoint does not imply support in every client.
Bloem keeps its organization, profile, Live TV and engagement features at Silo's
integration points through [owned integration adapters](../docs/architecture/bloem-upstream-adapters.md).

### Organizations, households and access policy

- **Organization administration:** organization lifecycle, ownership transfers,
  memberships, people and security management in the web console, with separate
  platform and organization administrative contexts and scoped audit visibility.
  Organization-owned resources, explicit resource entitlements and default
  resource bundles bound the media each viewer can access; a new organization
  receives the platform's creation-default bundle. See
  [multitenant administration](../docs/architecture/multitenant-administration.md),
  [resource ownership](../docs/architecture/resource-tenancy-foundation.md) and
  [tenant authorization](../docs/architecture/opa-tenant-authorization.md).
- **One organization per account:** an account's policy lives on Silo's own
  `users` columns, and its membership links it to exactly one organization
  (since `c60b7008e`, October 26, 2026). Tenant-aware enforcement covers
  browsing, playback, downloads and administration. The Silo-compatible
  `/api/v1` projection remains available alongside `/api/bloem/v1`; reviewed
  differences are documented in the
  [v1 compatibility policy](../docs/bloem/overlays/architecture/v1-scope.md).
- **Silo backend switching:** because account policy is where Silo keeps it, a
  database limited to one organization can be served by upstream Silo or Bloem,
  one at a time; `silo silo-switching enable` turns it on. See
  [Silo backend switching](../docs/architecture/silo-backend-switching.md).
- **Revisioned entitlement templates:** reusable playback, stream, profile,
  transcode, download, request, permission, quality and library policy for an
  organization or directly managed account. Browse-only, Viewer, Standard,
  Premium and Reseller Member starting points can be revised, cloned, archived,
  previewed and applied through the web console. See
  [entitlement templates](../docs/operations/entitlement-templates.md).
- **Bulk policy cohorts:** immutable selections and exact template revisions,
  profile-impact previews, derived policies and managed defaults, with resumable
  jobs for up to 10,000 snapshotted accounts and reconciliation against effective
  account/profile policy. Both platform and organization workflows are covered
  in the [bulk policy guide](../docs/operations/bulk-policy-cohorts.md).
- **Household profile credentials:** account holders can inspect, set, rotate and
  disable separate profile credentials in the web app using household/PIN authority
  and their current local account password. Revision checks preserve newer credentials
  and sessions when a stale write is rejected. Direct-profile login is limited to
  supported routes and clients; browser direct-profile login, shared-device profile
  pairing and SSO-only credential reauthentication remain unsupported. A database
  with a profile login cannot be switched to Silo.
- **Organization workflows:** scoped invitation creation, confirmed link regeneration
  and revocation, grant suspension/restoration/withdrawal, and redacted activity pages.
  Organization invitation links are shown once for manual delivery; this flow sends
  no email. Platform, organization and household authority remain separate.
- **Browse-only preview:** protected preview principals, signed guest sessions
  and catalogue hygiene let a preview account browse without playback. See the
  [browse-only policy](../docs/architecture/bloem-browse-only-content-policy.md);
  public preview is not enabled by installing it.

### Live TV and DVR

- **Live TV, OTA/DVR and programme guides:** an attributed adaptation of
  [Prairie Server](https://github.com/Prairie-Server/prairie-server), with
  HDHomeRun/Dispatcharr discovery and tuning, XMLTV guide ingestion, live
  playback, recording rules and recordings. The
  [source manifest](../docs/livetv/prairie-source-manifest.tsv) records its provenance.
- **Xtream live providers:** Bloem adds encrypted provider accounts, live channel
  import and provider-bound XMLTV guides. Watching and recording share connection
  limits across replicas; FFmpeg receives a protected MPEG-TS pipe rather than
  credentials. VOD/series, redirects and arbitrary stream URLs are not supported.
  See [Xtream setup and limits](../docs/architecture/xtream-live-tv.md).
- **Viewer-specific Live TV access:** a separate `watch_live_tv` permission,
  native capability discovery, web navigation and playback gates, server-side
  tuner-session heartbeat and release, and current profile/tenant authorization.
  See [Live TV client access](../docs/architecture/live-tv-client-access.md)
  for adapter coverage and remaining client integration requirements.
- **Web DVR workflows:** schedule from the guide or by channel/time, create recurring
  series rules and cancel recordings. Writes require readback; failed readback blocks
  further recording actions until explicit reload succeeds. Automatic catalog import, enforced retention
  and rule editing are not implemented. Actual tuner, Safari and replica owner-loss
  playback remain separate acceptance work.
- **Live TV across replicas:** a Live TV playlist or segment request that
  reaches another API replica is forwarded once to the process that opened the
  session's HLS bridge, found through Silo's node heartbeats. See [media ownership across replicas](../docs/architecture/bloem-cluster-media.md).

### Native storage

- **Storage-plugin libraries:** ebook, movie and audiobook libraries can read
  from storage plugins (S3, Bookwarehouse) alongside filesystem paths, with
  several locations and selected roots per library, managed from the web admin.
  Content requires immutable versions or verified snapshots. It is experimental;
  see [native storage](../docs/architecture/bloem-native-storage.md).

### Native apps, catalog and contracts

- **Richer Watch documents:** cast and crew, chapters, intro/credits/recap/preview
  markers, file editions, resolved posters, server-side search, person details
  and batch-resolved similar items. Viewer-scoped file IDs and episode navigation
  prevent inaccessible media leaking into documents; Continue Watching honors
  dismissed titles and episodes. See the
  [Bloem client surface](../docs/architecture/bloem-client-surface.md).
- **Generated Kotlin and Swift contracts:** server-derived DTOs, pinned source
  revisions and type-graph digests, playback fixtures, registry coverage checks
  and CI drift verification for Bloem's native clients. Generated
  settings bindings and effective settings values let apps consume the server's
  settings contract. Generation writes only this repository; adopting a contract
  is each client's own release. See [settings API](../docs/settings-api.md) and
  [client DTO generation](../docs/specs/client-dto-generator.md).
- **Explicit identity and compatibility contracts:** server identity and
  capability discovery, support for `X-Bloem-*` device headers alongside the
  Silo headers, Silo Apple v3 playback wire-shape translation, and
  contract-conformance and install/scan acceptance suites
  (`internal/clientcontract`, `internal/acceptance`). Client integrations use
  advertised capabilities rather than assuming every fork or app has the same
  features.
- **Book requests:** audiobook and ebook request lanes beside Silo's movie and
  series requests; see [audiobook requests](../docs/architecture/bloem-audiobook-requests.md)
  and [ebook requests](../docs/architecture/bloem-ebook-request-foundation.md).

### Announcements, promotions and seasonal presentation

- **Admin-authored announcements:** publish, preview and withdraw messages in
  the server web console, targeted to all viewers, roles, organizations,
  library access or explicit recipients. Messages carry severity, optional
  artwork/actions and expiry; the inbox shows their full text. Basic authoring
  does not require Garden, a separate Bloem application. See
  [server announcements](../docs/architecture/admin-announcements.md).
- **Promotional cards:** platform administrators create, edit and delete campaigns
  under **Campaigns & seasonal packs**, with audience/placement controls, schedules,
  artwork upload and review before publication. Home promotions default off and are
  hidden for children; detail and pre-playback cards preserve immediate continuation
  and the active player. Registries use last-write-wins, without revision locking.
- **Timed playback overlays:** eligible adult profiles can receive chapter-triggered
  artwork or muted video cards, with configurable duration, expiry, dismissal
  and a save-to-inbox action. Overlays respect playback controls and subtitle
  presentation and never pause content or take audio focus. The server web console
  and Garden can author campaigns, and the web app renders them; native rendering
  belongs to each client. See [playback overlays](../docs/architecture/playback-overlays.md).
- **Seasonal ambience:** an asset/pack registry, branding and capability
  projections, and annual schedules evaluated on the server in the selected
  timezone. The server web console and Garden manage schedules; clients receive
  concrete activation windows. Web snow and banner/sprite artwork honor expiry,
  reduced motion, playback suppression and a device-local off switch. Authenticated
  Home includes only public and current-organization packs; sign-in stays public-only.
  Campaign and seasonal uploads go to the catalog Assets store, so they work on
  local artwork storage as well as S3; the public S3 client is only a fallback
  when no Assets store is configured (since `f293cce0d`). See
  [seasonal scheduling](../docs/architecture/seasonal-scheduling.md).
- **In-process evaluation:** promotions and ambience are chosen by evaluators
  inside the server (`promotions.Engine`, `ambience.Engine`); an evaluation
  failure omits the optional presentation. See
  [presentation evaluators](../docs/architecture/bloem-presentation-evaluators.md).
- **Bloem push integration:** device notification registration defaults to
  Bloem's push relay rather than Silo's service.

### Playback control, security and cluster operation

- **Capability-gated remote control:** authorized administrators and primary
  household profiles can control participating live sessions, including pause,
  seek, volume and track changes. Admin-only termination and replanning have
  audited command state; replan constraints persist across API instances and
  can only narrow the client's negotiated capabilities. Delivery requires the
  instance holding the target socket and an app that advertises the command.
  See [remote control](../docs/architecture/admin-remote-control.md).
- **Delivery-time access checks:** native media delivery, the standalone proxy
  and notification fanout recheck library access where bytes or rows leave the
  server. See [delivery boundary enforcement](../docs/architecture/delivery-boundary-enforcement.md).
- **Scoped media credentials and outbound safeguards:** short-lived,
  purpose-scoped tickets replace general credentials in relevant URL transports.
  Bloem's outbound request policy (`internal/outbound`) guards Bloem's integrated
  fetch paths and refuses IPv6 special-use ranges that Silo's guard allows; Silo's
  webhook destination guard covers webhooks.
- **Single-node SQLite user state:** with `userdb.backend=sqlite`, a cluster guard
  fences local user state to one node.
- **Artwork job priority:** poster-family image-cache jobs are claimed first
  (migration `20260821175101`), alongside the scanner and artwork optimizations
  inherited from Silo.
- **Host telemetry:** an administrator host-stats endpoint reports CPU, memory
  and network use in addition to upstream task, log and node diagnostics.

### Product identity and maintenance

- **Bloem branding:** original icons, wordmark, repository artwork and public
  naming, with build-time web copy replacement (`web/bloem-brand-plugin.ts`) that
  preserves upstream module, environment and protocol identifiers. See
  [fork provenance](../FORK.md) and the [brand assets](../assets/brand/README.md).
- **Bloem plugin ecosystem:** a separately maintained SDK, catalog and
  first-party plugin set alongside the inherited host plugin runtime, and Bloem's
  own storage-provider protocol (`proto/bloem/plugin/v1`).
- **Continuous upstream tracking:** the daily synchronization workflow, conflict
  review and CI checks that bind a merge to the upstream revision actually
  tested, plus maintained operator and viewer guides.

## What Bloem removed, and why

Bloem removes its own additions when Silo comes to cover the need, or when an
addition no longer earns its merge cost. Each removal below is in the code at
`e852f92bf`, and each migration is forward-only. The linked pages record what
stays in the database.

- **Jellyfin and Audiobookshelf compatibility, then restored.** Bloem removed
  both on October 13, 2026 (`b8ddec9bf`), which took the seam ledger to 852
  entries. On
  October 18 it went back to running Silo's servers as Silo ships them
  (`2393c41ee`), on their own ports, because carrying the removal cost hundreds
  of deletion seams on every merge. See [protocol compatibility](../docs/architecture/bloem-compatibility-removal.md).
- **Companion-application enrollment and the compatibility gateway.** Bloem's
  enrollment of companion instances and its same-origin compatibility gateway
  on the public port went out with the October 13 removal and stayed out when
  Silo's servers returned. Live TV through the Jellyfin surface went with them.
- **Pastime managed tracking** (merge `4835a698e`, October 14). The tracking
  bridge, broker, list, calendar, recommendation and library bridges needed
  private triggers and direct writes into Silo tables. Pastime may return only as
  an ordinary plugin on Silo's public capabilities. See
  [Pastime removal](../docs/architecture/bloem-pastime-removal.md).
- **The header-authenticated media deployment gate** (`545ed3a33`, October 9).
  Bloem negotiates header-authenticated media whenever a client asks, as Silo
  does. See the [superseded rollout page](../docs/architecture/header-authenticated-media-rollout.md).
- **v1 socket audience tickets** (`a7436b4e5`, October 25). No Bloem client used
  the v1 sockets; they run Silo's handshake again.
- **Membership-owned account policy** (`c60b7008e`, October 26). Policy moved
  back onto `users`, its three-phase rollout and the `silo membership-policy`
  command went, and Silo switching became one flag.
- **Music** (`f9e1447f3`, October 26). Silo has no music library type, and
  production had no music libraries. The four `/api/bloem/v1/music/*` routes and
  three music tables are gone.
- **Presentation worker processes** (`310c545f9`, October 26). The two bundled
  workers ran only Bloem's own code, without an operating-system sandbox or
  resource limits, so the process boundary isolated nothing.
- **Lifecycle idempotency receipts** (`b017bfe84`, October 26). Mutations follow
  Silo's retry-safety classes; commands with their own keys (tenant member
  creation, people and platform policy jobs, entitlement applies, book requests)
  still replay. See [retrying mutations](../docs/bloem-api-reference.md#retrying-mutations).
- **Dead code** (October 26): the never-set `playback.strict_reconstruct_admission`
  setting, media-auth metrics, an unused Rego rule, unused web Live TV hooks and
  unrun Playwright specs, and a `deadcode` pass over every command.
- **Temporary backports** end when Silo ships the same fix: Bloem's font
  extraction backport ended with the merge through `8b84639ed` (`d72d6d2d4`).

[FORK.md](../FORK.md#what-bloem-removed) has the same list as a table, and the
[changelog](../docs/bloem/CHANGELOG.md) has the detail.

## CI and the published image

- **Bloem CI** ([`bloem-ci.yml`](workflows/bloem-ci.yml)) runs on every pull
  request, on pushes to `main` and `apiv2`, weekly and on demand. It runs Silo's
  former CI jobs, adapted, plus Bloem's own gates: tenant identity and native
  authentication suites against disposable PostgreSQL, sharded Go tests, Go lint
  (changed-line lint that ignores lines identical to Silo's, `govulncheck`),
  contract checks (OpenAPI, route inventory, migration ledger, client DTOs,
  playback fixtures, the seam ledger), the scenario executor and the web build
  and tests. Silo's own `ci.yml` stays Silo's file apart from one line that
  skips its job outside Silo's repository, because it calls Silo's reusable
  workflow from Silo's `main`, which knows none of Bloem's gates.
- **Runners.** When the repository variable `BLOEM_SELF_HOSTED` is `true`,
  pushes to `main` and pull requests by members listed in `BLOEM_CI_MEMBERS` run
  on private, disposable self-hosted runners. These accept only a job carrying
  its own run's labels. Every other run, including every pull request from a
  fork, runs on GitHub's `ubuntu-24.04` runners. Workflow runs from outside
  contributors need approval.
- **Bloem Image** ([`bloem-image.yml`](workflows/bloem-image.yml)) runs after
  Bloem CI succeeds on a push to `main`, or on demand for `main`. It checks
  through the GitHub API that the exact commit passed Bloem CI on `main`. It then
  builds that commit with the web bundle CI built and uploaded
  (`frontend-dist-<sha>`) and pushes one tag, the full 40-character commit SHA:
  `ghcr.io/bloem-studios/bloem-server:<sha>`, for `linux/amd64`, with the
  revision in the image labels. It never moves `latest`. Silo's own image
  workflow follows Silo's CI and does not run here.

See [Bloem CI](../docs/bloem/ci.md) for the job list and tool pins.

## How production is deployed

Bloem's production deployments follow the same pattern, recorded in
dated receipts under [`docs/operations/`](../docs/bloem/README.md#deployment-receipts):

1. **Image.** Since October 27, 2026, production runs the image Bloem Image
   published for the commit; earlier releases were built locally from a
   `git archive` of the source. The image is loaded on the application host
   under a release tag and its runtime configuration is compared with the
   running release.
2. **Restore point.** Before the restart: a container snapshot, a named database
   restore point with its WAL segment archived, and dumps of the tables the
   migrations change. For the October 26 Stage A release, whose migrations
   reshaped account data, they were first rehearsed on a restored copy.
3. **Offline migrations.** A restart script refuses to run unless the live image
   is the expected previous release. It stops the server, applies the pending
   migrations while nothing serves, and starts the new image.
4. **Checks.** Readiness and health locally and publicly, zero pending
   migrations, the Silo switching state, account and membership counts, active
   streams and the first minutes of logs.
5. **Rollback.** Bloem's migrations are forward-only, so a release with
   migrations rolls back by restoring the database to the restore point together
   with the container snapshot, not by starting the old image on the new schema.
   A release without migrations rolls back by starting the previous image.

The database runs in its own container, separate from the application server,
with WAL archiving and a streaming standby. Since October 27, 2026, production
runs with `POSTGRES_TUNE=off` and the database is tuned directly; see
[PostgreSQL auto-tuning](#postgresql-auto-tuning).

## Features

This list combines Silo's inherited functionality with the Bloem additions
above; it does not claim that every feature originated in this fork. Everything
below is described in more depth in the
[admin guide](../docs/wiki/admin-guide.md) and the [user guide](../docs/wiki/user-guide.md).

**Libraries and metadata**

- **Plays your media, your way** — direct play when the device supports it, remux or hardware-accelerated transcode (including NVENC) when it doesn't.
- **Library types** — movies, series, mixed movies and series, audiobooks, podcasts, ebooks and manga, each pointing at one or more folders, and in Bloem storage-plugin locations too; a first-run wizard creates the admin account and the first library. There is no music library type.
- **Plugin-driven metadata** — match and enrich your libraries with providers like TMDB and TVDB, installed as plugins; local `.nfo` sidecar files are honoured and merged with provider data; a wrong match is fixed with *Identify* and the fix survives rescans.
- **Library monitoring and autoscan** — Linux local-library monitoring scans changed paths; external autoscan integrations and scheduled scans cover other sources. Network shares need scheduled scans or an external notifier; they are not monitored by inotify. See [real-time monitoring](../docs/architecture/realtime-monitoring.md).
- **File names that are not UTF-8** are indexed and played (Silo #2571; see [non-UTF-8 paths](../docs/architecture/non-utf8-paths.md)).
- **Collections and home sections** — manual collections, rule-based smart collections, and collection templates that sync from TMDB or MDBList (Silo removed Trakt); the admin chooses which rows the home screen offers, viewers hide and reorder them.
- **Search, calendar, people** — search across titles, people and descriptions (optionally backed by Meilisearch), a calendar of upcoming episodes and releases, and a page per actor, director or writer.
- **Per-profile watchlist, favourites and history**, with the option to remove entries or mark something unwatched.

**Playback**

- **A full player on every device** — audio and subtitle track selection, subtitle appearance settings, subtitle search and machine translation where the operator enables it, an automatic quality ladder that steps down when playback keeps buffering, chapters, skip intro, next-episode countdown and a sleep timer.
- **Editions** — a title that exists in more than one version (theatrical and extended, two languages) lets the viewer choose which to play.
- **Audiobooks and reading** — audiobooks with per-book position, speed control, chapters and a sleep timer; an EPUB/PDF/comic reader whose position follows the reader across devices. Silo labels its books support beta.
- **Watch together** — several people on different devices watch the same thing in sync with a shared pause.
- **Offline downloads** to phones and tablets when the operator allows it, per device, with separate controls for original and transcoded downloads.
- **Hardware acceleration that probes itself** — set it to *auto* and Bloem checks what the container can actually reach, falling back to software; VA-API/Quick Sync and NVIDIA come as Compose overlays.

**Households, accounts and access**

- **Household profiles** — multiple profiles per account, with per-profile watch state and parental controls: a PIN per profile and a rating ceiling for children.
- **Invitations and invite codes** — invite by email (no account exists until it is accepted) or hand out codes with a use limit and a policy.
- **Access groups** — named permission sets (download, request media, Live TV) attached to users; deleting a group moves its members to the default group, never leaving anyone without a policy.
- **Reusable access policy** — immutable entitlement-template revisions and organization policy cohorts make reviewed policy changes repeatable for one account or up to 10,000 snapshotted accounts, with separate controls for original downloads and transcoded downloads and either all libraries or an explicit library selection.
- **Devices** — every signed-in phone, TV and browser, with remote sign-out, for the operator and for each viewer; TVs can sign in with a code from the phone app.

**Requests, notifications and Live TV**

- **Requests** — viewers ask for titles the library does not have; the operator approves, declines or fulfils from a queue, and the requester is notified when the item appears. Bloem adds audiobook and ebook requests.
- **Notifications** — an in-app inbox, email, Discord, and HMAC-signed generic webhooks whose receivers are disabled automatically when they keep failing; push notifications on the phone apps.
- **Live TV, guide and DVR** — tune, record and show a programme guide from HDHomeRun tuners and Dispatcharr, discovered on the LAN or probed by address, with XMLTV guide sources and a Compose override for host networking. Bloem also supports authenticated Xtream live providers and their XMLTV guides. The original tuner/DVR subsystem is an attributed adaptation of Prairie Server (see above).

**Compatibility and clients**

- **Web app included** — a full-featured web client and admin interface ship with the server.
- **Native API integrations** — the Silo native `/api/v2` surface and Bloem-specific `/api/bloem/v1` extensions support clients with matching contracts. Jellyfin-compatible apps connect on the Jellyfin port (default 8096) and Audiobookshelf apps on 13378 when enabled.
- **Bloem's native apps** for phone, tablet and TV (`bloem-apple`, `bloem-android`) are developed separately against the native APIs above and are not yet released.
- **Watch sync** to and from outside services, and optional **AI-assisted features** such as description translation. AI providers are connected once under AI Services and each job chooses one (Silo #3106).
- **Presentation and accessibility** — operator-managed campaign and seasonal artwork, profile ambience, larger text, reduced motion and high contrast. Silo has one theme and removed its administrator color, font and custom-CSS overrides (Silo #2413).

**Operating it**

- **Fast setup** — one `docker compose up -d` brings up the whole stack; everything else is configured in the admin UI.
- **Server roles** — run everything on one machine (`integrated`) or split into `api`, `transcode` and `proxy` nodes sharing one database, with a Nodes page showing each worker's GPU, scratch disk and load.
- **Object storage** for artwork and downloads on S3, MinIO or Cloudflare R2.
- **Tasks, logs, diagnostics, maintenance and stats** — every background job with progress and history, a filterable server log, a one-click diagnostics bundle with credentials scrubbed, safe housekeeping tasks, and playback history that shows whether each play was direct or transcoded.
- **Self-migrating updates and PostgreSQL auto-tuning** — an `integrated` or `api` server applies pending migrations when it starts, the image is one `SILO_IMAGE` line away, and the bundled PostgreSQL can be tuned to the host (see Configuration below).
- **Plugins are trusted code** — an administrator installs them from the configured plugin catalogs, and Silo's approved community catalog stays off until an administrator turns it on. An installed plugin runs with the server's own access, without a sandbox; see [plugin trust](../docs/architecture/plugin-trust.md).

## Quick start

The shortest path to a running server, from the [admin guide](../docs/wiki/admin-guide.md).
You need Docker with Compose 2.24+, Git and OpenSSL, and a folder of media.

> [!WARNING]
> **Choose the image first.** The Compose file defaults `SILO_IMAGE` to
> `ghcr.io/bloem-studios/bloem-server:latest`, but the Bloem Image workflow never
> moves `latest`. On October 27, 2026, `latest` was a September 24 build, and the
> container package was not publicly pullable. Pin `SILO_IMAGE` to a full-SHA tag
> you can pull, or build the image from this checkout. See
> [container image selection](../docs/bloem/overlays/wiki/deployment/docker.md#container-image-selection).

```sh
git clone https://github.com/bloem-studios/bloem-server.git
cd bloem-server
cp .env.bloem.example .env
chmod 600 .env
printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
```

Back up `SECRET_KEY` somewhere that is not the server; it encrypts every stored
credential and a database backup does not contain it. Then open `.env` and set
the absolute path to your media and the image:

```dotenv
MEDIA_ROOT=/path/to/your/media
SILO_IMAGE=ghcr.io/bloem-studios/bloem-server:<full commit sha>
```

To build the image yourself instead, run `docker build -t bloem-server:local .`
and set `SILO_IMAGE=bloem-server:local`. The Dockerfile builds the web frontend
itself; Bloem CI does not exercise that path, because Bloem Image injects the
bundle CI already built.

```sh
docker compose up -d
```

Open **http://localhost:8090** (or `http://<server-ip>:8090` from another
machine). The setup wizard creates the administrator account, confirms the
server's address and adds the first library; scanning starts right after. For
hardware transcoding, invite flows, reverse proxies and everything else, keep
reading or go straight to the [admin guide](../docs/wiki/admin-guide.md).

## Deploy with Docker (recommended)

The easiest way to run Bloem is with Docker Compose 2.24 or newer. The default stack assumes you do
not already have PostgreSQL and Redis available, so it bundles PostgreSQL, Redis, FFmpeg, and the
application for a one-command start. `SILO_IMAGE` selects the application image; it keeps Silo's
name for upstream-configuration compatibility, and its default
`ghcr.io/bloem-studios/bloem-server:latest` is stale (see the warning under Quick start).

1. **Create a `.env` file**

   ```sh
   cp .env.bloem.example .env
   printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
     "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
   ```

   This replaces the development database password from `.env.example` and creates the key Bloem
   uses to encrypt stored credentials. Back up `.env` separately from PostgreSQL; losing
   `SECRET_KEY` makes those credentials unrecoverable.

2. **Set your media path and image**

   Edit `.env` and set:

   ```dotenv
   MEDIA_ROOT=/path/to/your/media
   SILO_IMAGE=ghcr.io/bloem-studios/bloem-server:<full commit sha>
   ```

   `MEDIA_ROOT` is the one value most users need to change. You can also override `SILO_DATA_ROOT` if you do not want bind mounts under `/opt/silo`, and change ports if the defaults conflict with something else on the host. `SILO_*` names are retained compatibility identifiers.

3. **Start the default integrated stack**

   ```sh
   docker compose up -d
   ```

   This starts PostgreSQL, Redis and Bloem Server. Open the web app at `http://localhost:8090`. Native client APIs use that same address. Silo's Jellyfin-compatible server is published on port 8096 and its Audiobookshelf-compatible server on 13378 (`JF_PORT` and `ABS_PORT` move them).

   For existing PostgreSQL or Redis infrastructure, use a Compose override that replaces the service URLs and bundled dependencies. Setting those URLs only in `.env` does not override the default stack. See [external services](../docs/bloem/overlays/wiki/deployment/docker.md#external-postgresql-and-redis).

   ### Optional Intel/AMD VA-API or Intel Quick Sync

   The default stack is CPU-only so it starts on hosts without `/dev/dri`. On a Linux host with
   `/dev/dri`, enable the device overlay:

   ```sh
   docker compose -f docker-compose.yml -f docker-compose.vaapi.yml up -d
   ```

   To make that the default for this installation, set:

   ```dotenv
   COMPOSE_FILE=docker-compose.yml:docker-compose.vaapi.yml
   ```

   ### Optional NVIDIA/NVENC

   GPU support is kept out of the default compose file so hosts without NVIDIA drivers work unchanged.

   Install the NVIDIA Container Toolkit and use a Docker Compose version with GPU reservation support before enabling this override.

   Use the optional override file when you want NVENC:

   ```sh
   docker compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d
   ```

   If you want this controlled from `.env`, set `COMPOSE_FILE`:

   ```dotenv
   COMPOSE_FILE=docker-compose.yml:docker-compose.nvidia.yml
   NVIDIA_GPU_COUNT=1
   ```

   Windows uses `;` instead of `:` between compose files.

   Then `docker compose up -d` will include the NVIDIA override automatically.

4. **Configure through the admin UI**

   Add libraries, users, metadata providers, and playback settings from the web interface.

### Bind Mount Layout

The deploy-oriented compose files use host folder mappings rather than Docker-managed volumes.

By default, data is stored under `/opt/silo`:

- `/opt/silo/postgres`
- `/opt/silo/redis`
- `/opt/silo/plugins`
- `/opt/silo/artwork`
- `/opt/silo/compat`
- `/opt/silo/transcode`
- `/opt/silo/download-artifacts`
- `/opt/silo/catalog-seeds`

The optional `search` profile also stores its index under `/opt/silo/meilisearch`.

Media is mounted read-only into the container at `/mnt/media` from the host path you set in `MEDIA_ROOT`.

### Optional Search Profile

PostgreSQL full-text search works without any optional services. Meilisearch is available when you
want its search provider:

| Profile | Command | Description |
|---|---|---|
| default | `docker compose up -d` | Integrated server plus bundled PostgreSQL and Redis |
| `search` | `docker compose --profile search up -d` | Add the optional Meilisearch service |

Before starting the `search` profile, set `MEILI_MASTER_KEY` in `.env` to the output of
`openssl rand -hex 32`. After Bloem starts, choose Meilisearch under **Admin > Settings > Search**,
set the URL to `http://meilisearch:7700`, enter the same key as the API key, test the connection,
and save. Restart Bloem, then rebuild the catalog search index from the same page. Bloem continues
to use PostgreSQL full-text search until you select Meilisearch.

### Distributed Examples

The main Compose file includes commented proxy and transcode service examples. Most single-host
installs should leave them commented because the integrated service already includes proxying and
transcoding.

Multi-host operators can use those examples as a starting point for a dedicated worker Compose
file connected to the deployment's shared PostgreSQL and Redis services.

Proxy nodes serve source downloads from the same absolute media paths used by direct playback.
Prepared-download work can also run on transcode nodes. Each selected transcode node retains its
result on node-local storage and exposes it only through Silo's authenticated internal artifact API;
the paired proxy relays those bytes, so no shared artifact mount is required. Dedicated transcode
nodes default to retaining prepared downloads in a protected directory inside the transcode volume
captured at process startup. `download.artifact_dir` overrides that location for both dedicated
transcode nodes and the integrated/API-local fallback, so mount the configured path on every process
that prepares downloads. Changing either artifact-path setting requires a restart. Downloads with a
configured server-wide or per-user bandwidth limit remain API-local so those aggregate limits stay exact.
Clients discover distributed delivery through `proxy_delivery` on the download capability response.
When it is true, they may opt into `GET` or `HEAD /api/v1/downloads/{id}/file-proxy` and
`/api/v1/direct-download-proxy`; those routes may return a temporary redirect to a proxy node. The
established `/file` and `/direct-download` routes keep serving bytes directly with their existing
status-code contract; for a node-local prepared artifact, the API itself performs the authenticated
relay on that fallback route.

### Deployment Notes

The default Compose stack bundles PostgreSQL and Redis for a fresh installation. To use existing infrastructure, replace the service URLs and bundled dependencies with the [external-service override](../docs/bloem/overlays/wiki/deployment/docker.md#external-postgresql-and-redis). Setting `.env` URLs alone does not replace the default Compose values. Keep the same project and override files when updating an existing installation.

Bloem is externally stateful by default rather than fully stateless. Durable application state lives in PostgreSQL. Redis only stores coordination and cache-style data. Bloem still writes transient transcode output locally under `/tmp/silo-transcode`. If you switch `userdb.backend=sqlite`, Bloem also becomes locally stateful at `/var/lib/silo/userdb`. SQLite cannot join transactional account/default-profile creation: unsupported providers are rejected before account, membership or filesystem side effects. Use PostgreSQL for those setup and invitation flows; profileless provisioning retains its existing behavior.

Existing installations crossing the September 19 policy-array and Xtream migrations
need a tested backup, quiesced writers, table/index rewrite headroom, adequate
migration time and recycled application pools. Upgrade the complete API/worker fleet
before adding providers. See the [migration and rollback requirements](../docs/operations/2026-09-19-xtream-deployment.md#upgrade-and-rollback-boundaries);
automatic startup migration is not a substitute for this preparation.

Use the Bloem binary for migration status and execution: its
[migration filesystem](../docs/architecture/bloem-upstream-adapters.md#migration-filesystem)
rewrites historical migrations a fresh database still replays. Bloem's own
migrations are forward-only; take a database restore point before an upgrade
that applies any. For a legacy SQLite migration, review the
[bridge preflight](../docs/architecture/sqlite-bridge-preflight.md) before
applying a schema-specific migration procedure.

## Configuration

Bloem requires `DATABASE_URL` and `SECRET_KEY` when running from source or against external
infrastructure. In the default Docker Compose path, the stack wires the database and Redis URLs
for you. All other settings — libraries, metadata providers, transcoding, users — are managed
through the admin UI after first launch.

### Server Modes

| Mode | Description |
|---|---|
| `integrated` | Full server: API + frontend + scanner + transcode (default) |
| `api` | Primary/control role; configured local transcode fallback may still be used |
| `proxy` | Stream proxy node that connects to the shared deployment database and Redis |
| `transcode` | HLS and prepared-download worker node that connects to the shared deployment database and Redis |

### PostgreSQL Auto-Tuning

The tuner is Silo's (`internal/database/postgres_tune.go`), and Bloem runs it
unchanged. The default Docker Compose stack does not require a checked-in
`postgresql.conf`; it turns on [pgtune](https://github.com/le0pard/pgtune)-style
OLTP tuning by default. Without the variable, tuning is off.

```yaml
POSTGRES_TUNE: auto
```

When enabled, Bloem connects with `DATABASE_URL` and applies recommendations with
`ALTER SYSTEM`, which writes to PostgreSQL's `postgresql.auto.conf` inside the
database data directory. Reloadable settings are applied immediately with
`pg_reload_conf()`. Settings that PostgreSQL marks as restart-only are written
too, and Bloem logs their names in a "postgres restart required" warning. The
warning repeats on every start and lists every restart-only setting the tuner
writes, even when PostgreSQL already runs with that value, so check
[`pending_restart`](../docs/bloem/overlays/wiki/deployment/docker.md#postgresql-auto-tuning)
before restarting. When a restart really is needed, restart PostgreSQL and the
server together during a quiet window:

```sh
docker compose restart postgres silo
```

The default Compose database user has the required PostgreSQL permissions. The
tuner sizes PostgreSQL from the **server's** CPUs and memory, not the database's.
For an external PostgreSQL server, set `POSTGRES_TUNE=off` and tune the database
directly, or set `POSTGRES_TUNE_CPUS` and `POSTGRES_TUNE_MEMORY` to the database's
own values and give the `DATABASE_URL` user `ALTER SYSTEM`. Bloem's production
database runs separately from the server, and production has run with tuning off
since October 27, 2026. See
[external PostgreSQL tuning](../docs/bloem/overlays/wiki/deployment/docker.md#external-postgresql).

For `POSTGRES_TUNE_MEMORY=auto`, Bloem uses the first trustworthy memory source:
a finite Docker cgroup limit, the read-only `/host/proc/meminfo` mount supplied
by the bundled Compose file, then `/proc/meminfo` with container safety guards.
Auto-detected memory is treated as a PostgreSQL budget, defaulting to 75% of
detected RAM so Bloem, Redis, plugins, transcodes, and the OS retain headroom.
`POSTGRES_TUNE_DB_SIZE=auto` queries `pg_database_size(current_database())` and
classifies the workload by comparing the database size to that memory budget.

Optional tuning overrides:

| Variable | Default | Description |
|---|---:|---|
| `POSTGRES_TUNE_PROFILE` | `oltp` | Tuning profile. Only `oltp` is currently supported. |
| `POSTGRES_TUNE_MEMORY` | `auto` | Server/container RAM, such as `8GB` or `32GB`; explicit values are used as-is. |
| `POSTGRES_TUNE_MEMORY_BUDGET_PERCENT` | `75` | Percent of auto-detected RAM used for PostgreSQL recommendations. |
| `POSTGRES_TUNE_CPUS` | `auto` | CPU count used for worker recommendations; `auto` is the server process's CPU count. |
| `POSTGRES_TUNE_STORAGE` | `ssd` | One of `hdd`, `ssd`, `san`, or `nvme`. |
| `POSTGRES_TUNE_DB_SIZE` | `auto` | Use `less_ram` when the database comfortably fits in RAM, `mid_ram`, or `greater_ram` for very large databases. |
| `POSTGRES_TUNE_CONNECTIONS` | `100` | PostgreSQL `max_connections`; automatically raised if Bloem's app pool is configured higher. |
| `POSTGRES_SHM_SIZE` | `8gb` | Docker `/dev/shm` size for the bundled PostgreSQL container. |

Advanced operators can still supply their own PostgreSQL configuration or
override these env vars. Set `POSTGRES_TUNE=off` when you do not want Bloem to
change PostgreSQL server settings. Settings already written with `ALTER SYSTEM`
remain in `postgresql.auto.conf`; reset those PostgreSQL parameters if you later
move fully to a custom `postgresql.conf`.

## Build from Source

If you prefer running Bloem without Docker:

1. **Install prerequisites**: Go 1.26.10+ (the patch `go.mod` names), Node.js 24+ for the frontend, pnpm 10.32.1, a C compiler and libvips development libraries for CGO, PostgreSQL 18 with pgvector, Redis, and FFmpeg.

2. **Configure the source process**

   ```sh
   cp .env.bloem.example .env
   printf '\nSECRET_KEY=%s\nDATABASE_URL=%s\nREDIS_URL=%s\n' \
     "$(openssl rand -base64 48)" \
     'postgres://silo:silo@localhost:5432/silo?sslmode=disable' \
     'redis://localhost:6379' >> .env
   ```

   Change the URLs when you use existing services instead of the bundled development defaults.

3. **Start PostgreSQL and Redis** (skip if you already have them running)

   ```sh
   docker compose up -d postgres redis
   ```

4. **Build and run**

   ```sh
   GOWORK=off make build
   ./silo
   ```

   The source process uses `PORT` from its environment or `.env`; without a value, it listens on `8080`. Docker Compose separately maps the host port (normally `8090`) to container port `8080`. Other settings are configured through the admin UI.

## Documentation

- [Bloem documentation index](../docs/bloem/README.md) — every Bloem-owned document, including Bloem editions of and overlays on upstream Silo documents, with historical pages marked.
- [FORK.md](../FORK.md) — provenance, the "Silo wins" rule, seams and overlays, what Bloem adds and removed; [TRADEMARK.md](../TRADEMARK.md) and [LICENSE](../LICENSE).
- [Integration changelog](../docs/bloem/CHANGELOG.md) and [upstream adapters](../docs/architecture/bloem-upstream-adapters.md) — what each merge brought in and where Bloem keeps its extensions.
- [October 27 deployment receipt](../docs/operations/2026-10-27-main-deployment.md) — the revision production runs, its image, migrations, checks and limits.
- [Bloem CI](../docs/bloem/ci.md) — jobs, the daily upstream sync, the image gate and runner placement.
- [Admin guide](../docs/wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback and transcoding, access policy, Live TV, maintenance and troubleshooting.
- [User guide](../docs/wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests, notifications, and connecting native clients.
- [Wiki index](../docs/bloem/overlays/wiki/index.md) — every operator- and viewer-facing page, including [Deploy Bloem with Docker](../docs/bloem/overlays/wiki/deployment/docker.md), [Entitlement Templates](../docs/wiki/admin/entitlement-templates.md), [Supported Media Folder Structures and Naming](../docs/architecture/media-naming.md), [Collection Templates](../docs/architecture/collection-templates.md), [Local NFO Metadata](../docs/architecture/local-nfo-metadata.md) and [Monitoring Stream Nodes](../docs/architecture/observability.md).
- [SDK compatibility](../docs/architecture/bloem-sdk-compatibility.md) — plugin protocol versions, generated Kotlin/Swift contracts and client adoption.
- [Xtream live providers](../docs/architecture/xtream-live-tv.md) — encrypted setup, XMLTV, connection limits, delivery boundaries and removal; [operator steps](../docs/wiki/admin-guide.md#210-live-tv).
- [Embedded-web coverage](../docs/architecture/bloem-web-feature-coverage.md) — implemented screens, authority boundaries, verification and remaining product limits.
- [Bloem native API](../docs/bloem-api-reference.md) — `/api/bloem/v1` extensions, distinct from upstream `/api/v2`; [client surface](../docs/architecture/bloem-client-surface.md) and [security foundation](../docs/architecture/bloem-security-foundation.md).
- [Bloem development guide](../docs/bloem/overlays/DEVELOPMENT.md) — building from source, tests, migrations and project layout; [CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations.
- `docs/architecture/` and `docs/operations/` — design notes and operator runbooks, including the [Bloem v1 compatibility policy](../docs/bloem/overlays/architecture/v1-scope.md), [entitlement-template operations](../docs/operations/entitlement-templates.md), [bulk policy cohorts](../docs/operations/bulk-policy-cohorts.md), [protocol compatibility](../docs/architecture/bloem-compatibility-removal.md) and the [Canonical Settings API guide](../docs/settings-api.md).
- [Apple Push Display Token](../docs/notifications-push-api.md) — notification enrichment contract.

## Reporting Issues

Client implementers can use the [Canonical Settings API guide](../docs/settings-api.md)
for contract discovery, contextual headers, remote scopes, effective reads, and
the admin projection.

If you are reporting a bug, install problem, or performance issue, start with the admin workflow and reproduction steps, not Claude/Codex analysis.

Please include:

- What you were trying to do
- Exact steps you took
- What you expected to happen
- What actually happened
- What exact action is slow or broken (`save`, `scan`, `browse`, `import`, `playback`, etc.)
- Whether it happens every time or only sometimes
- The library, media type, filter, setting, or value involved
- Version, branch, commit, and deployment details if you know them
- Screenshots, recordings, or log snippets if relevant

If you used Claude/Codex for debugging, put that under `Technical notes` at the end. Suspected files, SQL output, stack traces, and root-cause theories can be helpful, but only after the workflow and repro steps are clear.

Use this template:

```text
Goal:
Steps:
Expected:
Actual:
What is slow/broken:
Scope:
Version/branch:
Deployment:
Technical notes:
```

## Contributing & Development

Bloem Server is open source under the same terms as Silo. See
[Bloem development guide](../docs/bloem/overlays/DEVELOPMENT.md) for building from source in a dev workflow,
running tests, database migrations, and project layout, and
[CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations and merge
request guidance.

Since this is a tracking fork (see FORK.md), a change that belongs in Silo's
own scanner, catalog, playback, or plugin runtime is generally better
contributed upstream to [Silo Server](https://github.com/Silo-Server/silo-server)
directly, where it benefits every downstream project, not just this one. Silo
currently accepts pull requests only from contributors who have had one merged
in a Silo repository or when a maintainer asks, so a clear issue is usually the
better route; see Silo's root `README.md`.
Contributions to Bloem-specific areas — the `/api/bloem/v1` tenant foundation, the
plugin SDK/catalog, or the client-contract test suites — belong here.

## License & Trademarks

Silo's source code is licensed under the **GNU Affero General Public License
v3.0 or later** (`AGPL-3.0-or-later`) — see [LICENSE](../LICENSE).

The **Silo name, logo, and wordmark are trademarks of Silo Media L.L.C.** and
are **not** covered by the AGPL. You're free to fork and redistribute the code,
but forks and redistributions must not use the Silo brand as their identity and
must remove or replace the brand assets. Publishing a Silo-branded app to an app
store requires written permission. See [TRADEMARK.md](../TRADEMARK.md) for what's
permitted — including referential use like "compatible with Silo."
