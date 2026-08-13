# Vondel Server — fork provenance

Vondel Server is a fork of [Silo Server](https://github.com/Silo-Server/silo-server),
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
module path renamed to `github.com/Vondel-Media/vondel-server`. That is a hard
fork, and it cost more than it bought: the rename touched 1,178 files, which
makes every future upstream change a conflict, while the features Vondel adds —
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
an urgent isolated fix, cherry-pick the exact upstream commit onto a Vondel
topic branch instead; the next full upstream merge will recognize that patch.
Never force-push shared Vondel branches or squash imported upstream history.

Vondel changes should remain focused downstream commits. Prefer additive
packages, migrations, routes, and build-time branding over broad rewrites of
upstream-owned files. When a merge conflict is unavoidable, preserve Silo's
public compatibility surface and reapply the smallest Vondel delta.

## What Vondel adds

- A private plugin SDK, catalog and first-party plugin set.
- Client contract conformance tests (`internal/clientcontract`) and an install
  and scan acceptance test (`internal/acceptance`).
- A tenant and identity foundation under `/api/v2`, while `/api/v1` remains
  the Silo-compatible surface.
- Product identity: Vondel naming in user-facing copy.

Live TV, OTA/DVR and EPG are designed and planned as additive packages ported
from the AGPL Prairie fork; they are not implemented on `main` yet.

## Ongoing merge cost

User-facing prose is rebranded at bundle time by `web/vondel-brand-plugin.ts`,
so upstream web source remains mergeable. Vondel's public icons, wordmark, PWA
manifest, service-worker default notification name, and the two asset paths in
`SiloBrand.tsx` are maintained explicitly because Silo's reserved visual assets
must not ship in a modified distribution. Compatibility identifiers, module
paths, environment variables, and protocol fields retain their Silo names.

Clients are NOT forks. `vondel-apple` and `vondel-android` are clean-room
projects built from documented server interfaces — see
`docs/superpowers/specs/2026-08-12-vondel-clean-room-clients-design.md`.
