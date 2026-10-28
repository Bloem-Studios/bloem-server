# Bloem Server — fork provenance

Bloem Server is a fork of [Silo Server](https://github.com/Silo-Server/silo-server),
AGPL-3.0, whose licence and copyright notices are preserved throughout. Silo is a
self-hosted media server (films, series, audiobooks, ebooks, podcasts and manga)
developed by the Silo-Server project; its own description is the root
[`README.md`](README.md), which stays exactly as Silo wrote it. Bloem's front page
is [`.github/README.md`](.github/README.md), which GitHub shows in place of the
root file.

The fork started on August 13, 2026 (`a51326741`) under the working name Vondel
Server and was renamed Bloem on August 24, 2026 (`b25394bc8`); older commit
messages still say "Vondel".

## This is a TRACKING fork, deliberately

`main` shares real history with upstream, so every upstream release can be
merged rather than ported by hand. Git remotes are local configuration and are
not copied by `git clone`; add the protected upstream remote in each maintainer
checkout:

```sh
git remote add upstream https://github.com/Silo-Server/silo-server.git
git remote set-url --push upstream DISABLED
```

An earlier attempt imported the tree as a zero-parent root commit with the Go
module path renamed to `github.com/Bloem-Studios/bloem-server`. That is a hard
fork, and it cost more than it bought: the rename touched 1,178 files, which
makes every future upstream change a conflict, while the features Bloem adds —
Live TV chief among them — are additive packages that merge cleanly without it.
`a51326741` replaced it with Silo's real history. Its commit message names a
`hard-fork-archive` branch for the earlier history; no such branch is published
in this repository (checked October 27, 2026).

**The module path stays `github.com/Silo-Server/silo-server`.** It is the price
of merge-ability and it is worth paying. Renaming is a one-way door available at
any time; staying merge-able is not recoverable once lost.

Maintainer checkouts deliberately set `upstream`'s push URL to an invalid
value. Nothing from this repository is pushed directly to Silo-Server;
upstream contributions use a separate fork and pull request.

## Taking upstream changes

Update the tracked fork with a normal merge so both projects' ancestry remains
visible and future merges can reuse Git's conflict resolutions:

```sh
git fetch --prune upstream
git switch main
git pull --ff-only origin main
git merge --no-ff upstream/main
```

**Silo wins.** In shared code, take Silo's side of every conflict Bloem can adapt
around, then reapply the smallest Bloem delta in Bloem-owned files. Bloem's
native clients are unreleased, so keeping them on an older wire shape is not a
reason to keep a Bloem difference ([agent rules](BLOEM_AGENTS.md#shared-implementation-follows-silo)).
Every Silo line Bloem keeps edited conflicts again on the next merge, and Silo
moves daily; adopting Silo's implementation also brings Silo's fixes and tests.

Run the complete verification suite before pushing the merge to `origin`: the
[validation checks](.github/CONTRIBUTING.md#validate-your-change), including the
web bundle budget and CI's changed-line lint for merges. The daily
`Upstream sync` workflow (`.github/workflows/upstream-sync.yml`, 05:20 UTC)
performs this merge on an `upstream-sync/<date>-<sha>` branch and opens a pull
request; `upstream-sync-merge.yml` merges it only when Bloem CI passed on that
exact head and the head is the untouched, conflict-free merge of a `main` commit
with a Silo commit. A conflicted merge is pushed as a draft pull request with
the conflict markers committed, for a person to resolve. See
[CI](docs/bloem/ci.md#upstream-sync-merges). For an urgent isolated fix,
cherry-pick the exact upstream commit onto a Bloem topic branch instead; the
next full upstream merge will recognize that patch. Never force-push shared
Bloem branches or squash imported upstream history.

Bloem changes should remain focused downstream commits. Prefer additive
packages, migrations, routes, and build-time branding over broad rewrites of
upstream-owned files. When a merge conflict is unavoidable, preserve Silo's
public compatibility surface and reapply the smallest Bloem delta.

## Seams and overlays

A **seam** is a Silo-owned file (one that also exists in `upstream/main`) that
Bloem modifies, deletes or type-changes. Each one is declared, usually with a
one-line reason, in [`contracts/seams.txt`](contracts/seams.txt).
`make verify-seams` (`scripts/verify-seams.sh`) fails when Bloem differs from
Silo in a file the ledger does not list; it compares with the upstream tip, the
last merged upstream commit and an in-progress merge's incoming commit, so it is
safe to run mid-merge. The count may only go down, and adding an entry is a
reviewed decision.

An **overlay** is the documentation equivalent: a Bloem-owned file under
[`docs/bloem/overlays/`](docs/bloem/overlays/) that records, section by section,
what Bloem adds to or replaces in the Silo document of the same path, so the
Silo document itself stays Silo's. The [documentation index](docs/bloem/README.md)
lists every overlay.

## What Bloem adds

- A tenant and identity foundation under `/api/bloem/v1`, while `/api/v1` remains
  the Silo-compatible surface. The shipped boundary includes organization
  lifecycle and membership management (with an admin UI for people and
  security administration), organization-scoped profile groups, a separate
  administrative-context session system, and OPA-bounded visibility of
  organization-owned or explicitly entitled media folders; see
  [the operator runbook](docs/architecture/opa-tenant-authorization.md). Since
  October 26, 2026 (`c60b7008e`) account policy lives on Silo's `users` columns
  and a membership only links an account to its one organization.
- [Silo backend switching](docs/architecture/silo-backend-switching.md): a
  database limited to one organization can be served by Silo or Bloem, one at a
  time.
- Direct-profile login for supported native routes, plus household credential
  management. Browser direct-profile login and shared-device profile pairing
  remain unsupported; account-device activation is a separate protocol.
- A native client API surface built for Bloem's own Android/Apple clients:
  richer Watch documents (cast/crew, chapter and skip-intro markers, file
  editions, server-side search, poster resolution), a person-detail
  endpoint, and a batch-resolved similar-items endpoint. Verified by client
  contract conformance tests (`internal/clientcontract`) and an install/scan
  acceptance test suite (`internal/acceptance`).
- [Native storage](docs/architecture/bloem-native-storage.md): ebook, movie and
  audiobook libraries that read from storage plugins (S3, Bookwarehouse)
  alongside filesystem paths. It remains experimental.
- A separately maintained plugin SDK, catalog and first-party plugin set.
- Product identity: Bloem naming in user-facing copy.
- Embedded-web organization workflows, household profile-credential management,
  platform campaign/seasonal authoring and native Live TV/DVR controls. The
  [coverage matrix](docs/architecture/bloem-web-feature-coverage.md) records the
  shared seams, tested behavior and remaining limits; the
  [completion handoff](docs/architecture/bloem-web-completion-handoff.md) is the
  historical September record of that work.
- Encrypted Xtream live-provider consumption and provider-bound XMLTV, with
  shared physical-connection budgets and server-owned encoder input. This is
  Bloem's addition to the Prairie-derived subsystem, not a Silo feature or a
  general remote-URL player. See [Xtream scope](docs/architecture/xtream-live-tv.md).
- Explicit [downstream contract adjudications](docs/architecture/bloem-contract-adjudications.md)
  that preserve SHA-pinned upstream scenario and route snapshots while testing
  reviewed Bloem behavior. They do not weaken production authorization or
  reintroduce credential disclosure.

Plugin authorization and adult-scene policy are separate increments.
Live TV, OTA/DVR, and EPG are supplied by an attributed AGPL adaptation of the
Prairie Server subsystem pinned in `docs/livetv/prairie-source-manifest.tsv`.

## What Bloem removed

Bloem has removed several of its own additions where Silo now covers the need or
where the addition no longer earned its merge cost. Each removal is
forward-only; the linked pages record what stays in the database.

| Removed | When | Why, and where it is recorded |
| --- | --- | --- |
| Companion-application enrollment and the same-origin compatibility gateway | October 13, 2026 (`b8ddec9bf`); not restored on October 18 | Removed with Bloem's first removal of Jellyfin and Audiobookshelf compatibility. When Bloem went back to running Silo's compatibility servers as Silo ships them (`2393c41ee`), its own gateway stayed out. See [protocol compatibility](docs/architecture/bloem-compatibility-removal.md). |
| Pastime managed tracking | October 14, 2026 (merge `4835a698e`) | The bridge, broker and host hooks needed private triggers and writes into Silo tables. Pastime may return only as an ordinary plugin. See [Pastime removal](docs/architecture/bloem-pastime-removal.md). |
| Header-authenticated media deployment gate | October 9, 2026 (`545ed3a33`) | Bloem negotiates header-authenticated media whenever a client asks, as Silo does. See the [superseded rollout page](docs/architecture/header-authenticated-media-rollout.md). |
| Membership-owned account policy and its rollout phases | October 26, 2026 (`c60b7008e`) | Policy back on `users`, where Silo keeps it; 13 seams returned to Silo's bytes. See the [changelog](docs/bloem/CHANGELOG.md#october-26-2026--account-policy-back-on-users). |
| Music catalog | October 26, 2026 (`f9e1447f3`) | Silo has no music library type; production had no music libraries. |
| Bundled presentation worker processes | October 26, 2026 (`310c545f9`) | The workers ran only Bloem's own code, without a sandbox, so the process boundary isolated nothing. See [presentation evaluators](docs/architecture/bloem-presentation-evaluators.md). |
| Lifecycle idempotency receipts | October 26, 2026 (`b017bfe84`) | Mutations follow Silo's retry-safety classes; commands with their own keys still replay. |
| v1 socket audience tickets | October 25, 2026 (`a7436b4e5`) | No Bloem client used the v1 sockets; Silo's handshake is back. |

The [integration changelog](docs/bloem/CHANGELOG.md) has the full list, including
dead-code removals.

## Current integration boundary

At `5fc698915` (October 28, 2026), Bloem `main` contains all of Silo `main`
through `564b906b8` (2026-10-28 02:44 UTC), merged in `5fc698915`, and the
ledger declares **305 seams**. Production runs `aeeea2357`, which has Silo
through `73a6ae6a3`: the
[October 27 receipt](docs/operations/2026-10-27-main-deployment.md) records that
deployment and the ones before it, starting with `422a51fed`, the first
production image built and published by CI. A source
integration is not a deployment, and a deployment is not native-client or
playback acceptance. Profile-aware playback/download APIs, migration adaptation,
web branding and bundle allowances stay in owned files. The
[adapter guide](docs/architecture/bloem-upstream-adapters.md) records the durable
contracts; the [changelog](docs/bloem/CHANGELOG.md) records each integration.

Use the Bloem binary for migration status and execution. Its `migrations.BloemFS`
adapter rewrites historical migrations a fresh database still replays before
`20261026003000_bloem_account_policy_on_users` moves account policy back onto
`users`, where Silo keeps it.

A Silo database taken over by Bloem can stay switchable: with
`public.bloem_silo_switching` enabled, either binary can serve it, one at a
time. Upstream merges that add Silo writes to tables Bloem constrains need a
switching test and a passing `make silo-switch-check`; see
[Silo backend switching](docs/architecture/silo-backend-switching.md).

History, kept as history: on October 7, 2026 production ran Bloem `c87b44545`,
with Silo through `74158b4a8` and 407 declared seams
([October 7 record](docs/operations/2026-10-07-native-storage-deployment.md)).
The October 5 merge-reduction pass had reduced the ledger to 390 paths, and a
later extraction to 387, before native onboarding and the October 7 integration
added reviewed seams. The October 13–18 removal of Jellyfin and Audiobookshelf
needed 852 entries; restoring Silo's servers took it to 388, the October 23 merge
to 347, and the October 25–26 simplifications from 350 to 306. The September
deployment records are historical evidence too, and no deployment record
changes the normal merge policy or certifies future builds.

## Ongoing merge cost

The seam ledger is the merge cost: 306 Silo-owned files differ at `e852f92bf`,
most by a hook line or a generated artifact (the route inventory, OpenAPI and
migration ledger necessarily differ because Bloem registers more routes).

User-facing prose is rebranded at bundle time by `web/bloem-brand-plugin.ts`,
so upstream web source remains mergeable. Bloem's public icons, wordmark and repository artwork in
`assets/` are owned visual assets; the Silo paths it replaces with Bloem art
(`assets/icon.png` and the icons under `web/public/`) are declared seams. The build adapter supplies the PWA manifest
names, service-worker default notification name and built-in component image
paths in development and production output while their shared source stays
identical to upstream. Silo's reserved visual assets must not ship in a modified
distribution.
`assets/`, `assets/brand/` and `web/public/` each carry a `NOTICE` recording that
the brand assets are original Bloem work rather than derivatives of Silo's visual
identity. Compatibility identifiers, module paths, environment variables, and
protocol fields retain their Silo names.

Clients are NOT forks. `bloem-apple` and `bloem-android` are clean-room
projects built from documented server interfaces — see
`docs/superpowers/specs/2026-08-12-bloem-clean-room-clients-design.md`.

## Where Bloem's documentation lives

Silo-owned files stay as close to upstream as possible. Bloem's own material is in
Bloem-owned files indexed at [docs/bloem/README.md](docs/bloem/README.md): the Bloem front
page ([.github/README.md](.github/README.md)), contribution rules
([.github/CONTRIBUTING.md](.github/CONTRIBUTING.md)), agent overrides
([BLOEM_AGENTS.md](BLOEM_AGENTS.md)), and per-document overlays under `docs/bloem/overlays/`
that record what Bloem adds to or replaces in an upstream document of the same path.

## Prairie Server Live TV

Bloem's server-side and web Live TV, OTA tuner, guide, streaming, and DVR
subsystem is adapted from [Prairie Server](https://github.com/Prairie-Server/prairie-server)
at commit `095ecd22fbea3384a905eb9049386015db3ff4d8`, licensed under
AGPL-3.0. Bloem preserves the applicable license and identifies every imported
or adapted source blob in `docs/livetv/prairie-source-manifest.tsv`.

The adaptation changes product/module identity, migration ordering, and shared
Bloem integration points. It does not import Prairie native-client source,
assets, layouts, or tests.
