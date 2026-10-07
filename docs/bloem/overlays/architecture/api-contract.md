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


## Bloem native storage and generated contracts

The October 7 source baseline, Bloem
`c87b44545228c93f909275c0b5c4fc1d6a289e1b` with Silo through `74158b4a8`, keeps
`/api/v2` separate from `/api/bloem/v1`. Native-storage administration is a
Bloem-owned protected platform/organization surface. Its route and wire
reference is [Native Storage Administration](../../../bloem-api-reference.md#native-storage-administration),
with all 32 scope-specific operations also generated into
[the native OpenAPI artifact](../../../../contracts/api/bloem/v1/openapi.json).
These are document-only chi declarations, not the Silo OpenAPI operation inventory
or additional runtime registrations. Its composition-dependent capability
schema never promotes artifact approval or synthetic provider tests to
`backend_verified`.

Bloem does wrap a finite set of ordinary v1/v2 operations to contain unsupported
native mutations. V2 adapters preserve `native_library_delete_unsupported`,
`native_repair_unsupported`, `native_local_operation_unsupported` and
`native_storage_unavailable` as problem codes. Complete selected target sets
are authorized before classification, with fresh checks at later selected
mutation phases. These are reviewed downstream boundaries, not upstream
claims or a global SQL/policy-generation lease. The
[onboarding architecture](../../../architecture/bloem-native-storage-onboarding.md#publication-and-compatibility-boundaries)
records the limits.

Generated OpenAPI/DTO agreement establishes a contract snapshot, not whether
an optional handler is mounted, an authenticated workflow passed against a
production server, or a native client uses it. Feature detection remains
required. The public plugin SDK version, private `StorageProvider` wire schema
and native-client API artifacts are separate compatibility surfaces; see
[protocol ownership](../../../architecture/bloem-native-storage.md#ownership-and-protocol).
