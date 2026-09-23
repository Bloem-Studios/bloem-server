# Bloem overlay: Native API contract: Huma and `/api/v2`

Bloem additions and overrides for the upstream Silo document [`docs/architecture/api-contract.md`](../../../architecture/api-contract.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Migration ledger”. **Bloem replaces** the corresponding upstream passage with:

The second kind is curated by hand and is what the ledger exists to hold: `consumers` and
`consumer_call_sites` (including `match: manual` and `match: follower` sites), `section`, `release_flow`, `tier`, `disposition`,
`disposition_rule`, `disposition_rationale`, `owner`, `review_state`, `v2`, and `notes`. The
schema ties them together: each `disposition_rule` names the document or decision that justifies
it and is therefore allowed only with the disposition it justifies (`maintainer_decision` is the
escape hatch for any); `removed` and `documented_exclusion` rows carry an all-null `v2`; a
`ported`, `redesigned`, or `replaced` row can be `ratified` only with a complete `v2` target,
except the four `contract_root_probes` rows, which are ratified as retained unversioned probes
with `v2` unset and `notes` opening `Retained as unversioned probe on <listener> listener`; and
every `removed`, `redesigned`, or `replaced` row names an `owner`, which the gate refuses to
ratify while it is a placeholder (anything starting with `pending`, or `tbd`, `todo`, `unknown`,
`none`, compared case-insensitively). Tier is a rule, stated in the file's `description` and
enforced by the gate: tier 1 is the plan's release-critical flows unless the row is `removed`,
since a removed route has no v2 behavior to baseline, so a removed row in tier 1 fails.
`release_flow` is derived from route intent, not path prefix: the acting-admin library
management routes under `/api/v1/libraries/`, the admin scan triggers, and the theme catalog
refresh are `core_admin`, while the viewer-facing `/api/v1/library/{id}/*` reads are
`browse_search`. Proxy and transcode-node rows that are `ported` keep `v2` null by design: those
listeners have no `/api/v2` namespace, so the route is retained at its version-neutral path and
described through the raw-operation registry, never aliased into v2. Ratifying such a row therefore
means ratifying its retention, not a mapping: the schema's node-listener rule requires a ratified
`proxy` or `transcode_node` port to keep `v2` unset, carry `disposition_rule` `listener_delegation`,
and open its `notes` with `Retained on <listener> listener with <auth class>`, citing the
`x-silo-worker-protocols` entry that describes it; on the `api` listener a ratified port still
names its v2 operation completely. Because these fields are decisions rather than derivations,
no generator ever writes them: CI checks the committed file and nothing invents a disposition.

The copied fields are the opposite case. Adding one route shifts every later inventory row,
renumbers the inventory's middleware-chain table, and leaves the new row with no entry at all,
so a hand-maintained file goes red on a change that contains no decision. `make migration-ledger`
(`scripts/apiv2-ledger/refresh_ledger.py`) does that bookkeeping: it re-merges the ledger against
the current inventory by key, refreshing only the copied fields and the row order, seeding an
entry for each new route, and reassigning sections. Every curated field and every committed call
site is preserved verbatim, and re-running on an unchanged inventory rewrites the file byte for
byte, which is what `make verify-migration-ledger` checks before it runs the Go gate. A seeded row
arrives `proposed` with no owner and no `v2` target: it is a placeholder for a decision, not a
decision, and the section PR still has to make it. Refreshing consumer evidence is a separate
operation and is not part of this target -- it needs the sibling client trees at a named commit
(see `scripts/apiv2-ledger/README.md`).
