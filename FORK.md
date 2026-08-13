# Vondel Server — fork provenance

Vondel Server is a fork of [Silo Server](https://github.com/Silo-Server/silo-server),
AGPL-3.0, whose licence and copyright notices are preserved throughout.

## This is a TRACKING fork, deliberately

`main` shares real history with upstream and carries an `upstream` remote, so
every upstream release can be merged rather than ported by hand.

An earlier attempt imported the tree as a zero-parent root commit with the Go
module path renamed to `github.com/Vondel-Media/vondel-server`. That is a hard
fork, and it cost more than it bought: the rename touched 1,178 files, which
makes every future upstream change a conflict, while the features Vondel adds —
Live TV chief among them — are additive packages that merge cleanly without it.
That history is preserved on the `hard-fork-archive` branch.

**The module path stays `github.com/Silo-Server/silo-server`.** It is the price
of merge-ability and it is worth paying. Renaming is a one-way door available at
any time; staying merge-able is not recoverable once lost.

`upstream`'s push URL is deliberately set to an invalid value. Nothing here is
ever pushed to Silo-Server; contributions go via a fork and a pull request.

## What Vondel adds

- Live TV, OTA/DVR and EPG — a permanent non-goal upstream, ported from the
  AGPL Prairie fork. New packages, so merge cost is near zero.
- A private plugin SDK, catalog and first-party plugin set.
- Client contract conformance tests (`internal/clientcontract`) and an install
  and scan acceptance test (`internal/acceptance`).
- Product identity: Vondel naming in user-facing copy.

## The one ongoing cost

Rebranding user-facing copy touches ~87 web files, and those are exactly the
files upstream also edits, so they are where merge conflicts will appear. The
fix is to render the product name from a single constant rather than inlining
it, which would also be a good upstream contribution. Until then, expect string
conflicts on UI-heavy merges and resolve them in favour of Vondel naming.

Clients are NOT forks. `vondel-apple` and `vondel-android` are clean-room
projects built from documented server interfaces — see
`docs/superpowers/specs/2026-08-12-vondel-clean-room-clients-design.md`.
