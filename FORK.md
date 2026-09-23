# Bloem Server — fork provenance

Bloem Server is a fork of [Silo Server](https://github.com/Silo-Server/silo-server),
AGPL-3.0, whose licence and copyright notices are preserved throughout.

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
That history is preserved on the `hard-fork-archive` branch.

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

Run the complete verification suite before pushing the merge to `origin`. For
an urgent isolated fix, cherry-pick the exact upstream commit onto a Bloem
topic branch instead; the next full upstream merge will recognize that patch.
Never force-push shared Bloem branches or squash imported upstream history.

Bloem changes should remain focused downstream commits. Prefer additive
packages, migrations, routes, and build-time branding over broad rewrites of
upstream-owned files. When a merge conflict is unavoidable, preserve Silo's
public compatibility surface and reapply the smallest Bloem delta.

## What Bloem adds

- A tenant and identity foundation under `/api/bloem/v1`, while `/api/v1` remains
  the Silo-compatible surface. The shipped boundary includes organization
  lifecycle and membership management (with an admin UI for people and
  security administration), organization-scoped profile groups, a separate
  administrative-context session system, and OPA-bounded visibility of
  organization-owned or explicitly entitled media folders; see
  [the operator runbook](docs/architecture/opa-tenant-authorization.md).
- A companion-deployment gateway: enrollment, trust, and administration for
  companion instances running behind this server, with a hardened
  default-deny posture (details deliberately kept out of this document).
- Direct-profile login for supported native routes, plus household credential
  management. Browser direct-profile login and shared-device profile pairing
  remain unsupported; account-device activation is a separate protocol.
- A native client API surface built for Bloem's own Android/Apple clients:
  richer Watch documents (cast/crew, chapter and skip-intro markers, file
  editions, server-side search, poster resolution), a person-detail
  endpoint, and a batch-resolved similar-items endpoint. Verified by client
  contract conformance tests (`internal/clientcontract`) and an install/scan
  acceptance test suite (`internal/acceptance`).
- A private plugin SDK, catalog and first-party plugin set.
- Product identity: Bloem naming in user-facing copy.
- Embedded-web organization workflows, household profile-credential management,
  platform campaign/seasonal authoring and native Live TV/DVR controls. The
  [coverage matrix](docs/architecture/bloem-web-feature-coverage.md) records the
  shared seams, tested behavior and remaining limits; the
  [completion handoff](docs/architecture/bloem-web-completion-handoff.md) records
  the next acceptance milestone.
- Encrypted Xtream live-provider consumption and provider-bound XMLTV, with
  shared physical-connection budgets and server-owned encoder input. This is
  Bloem's addition to the Prairie-derived subsystem, not a Silo feature or a
  general remote-URL player. See [Xtream scope](docs/architecture/xtream-live-tv.md).
- Explicit [downstream contract adjudications](docs/architecture/bloem-contract-adjudications.md)
  that preserve SHA-pinned upstream scenario and route snapshots while testing
  reviewed Bloem behavior. They do not weaken production authorization or
  reintroduce credential disclosure.

The [September 19 deployment record](docs/operations/2026-09-19-xtream-deployment.md)
identifies the deployed revision and the narrowly approved CI-timeout exception.
That exception does not change the upstream merge policy or certify future builds.

Plugin authorization and adult-scene policy are separate increments.
Live TV, OTA/DVR, and EPG are supplied by an attributed AGPL adaptation of the
Prairie Server subsystem pinned in `docs/livetv/prairie-source-manifest.tsv`.

## Ongoing merge cost

User-facing prose is rebranded at bundle time by `web/bloem-brand-plugin.ts`,
so upstream web source remains mergeable. Bloem's public icons, wordmark, PWA
manifest, service-worker default notification name, the repository artwork in
`assets/`, and the two asset paths in `SiloBrand.tsx` are maintained explicitly
because Silo's reserved visual assets must not ship in a modified distribution.
Both brand-asset directories carry a `NOTICE` recording that they are original
Bloem work rather than derivatives of Silo's visual identity. Compatibility
identifiers, module paths, environment variables, and protocol fields retain
their Silo names.

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
