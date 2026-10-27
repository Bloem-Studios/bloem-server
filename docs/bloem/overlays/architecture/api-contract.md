# Bloem overlay: Native API contract: Huma and `/api/v2`

**What this overlays.** Silo's
[`docs/architecture/api-contract.md`](../../../architecture/api-contract.md). Bloem
replaces one sentence of the section “Migration ledger”, adds a paragraph after
it, and adds a closing section on Bloem's separate native contract. An
*overlay* is a Bloem-owned page that records Bloem's additions to, or
replacements of, sections of a Silo-owned document. The Silo file stays
exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Bloem adds routes to Silo's route groups, and its own
middleware changes the chain of almost every route, so its route inventory
moves on most merges. To keep the hand-curated migration ledger
(`contracts/api/v2/migration.json`) in step without hand edits, Bloem added
`make migration-ledger` (`scripts/apiv2-ledger/refresh_ledger.py`, in
`23b23f531`, 2026-09-16). Bloem also publishes a second contract,
`/api/bloem/v1`, which Silo's document does not cover. Bloem first documented
these in the Silo file in September 2026 (`0f00b3eae`, 2026-09-01) and moved
the text here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`Makefile`, `Makefile.bloem`, `scripts/apiv2-ledger/`,
`contracts/api/bloem/v1/openapi.json`). Silo has not changed this document since
the previous review (Silo `6de6d5e5e`). The earlier edition of this overlay
replaced Silo's whole description of the ledger tooling. Silo's section
assignment and consumer-evidence scripts still run in Bloem, so the
replacement is now narrowed to the one sentence that differs.

Index: [Bloem documentation](../../README.md).

---

**Location:** section “Migration ledger”, the paragraph beginning “The second
kind is curated by hand…”, its closing sentence “Because these fields are
decisions rather than derivations, the ledger is gated, not regenerated: CI
checks the committed file, and nothing rewrites it.” **Bloem replaces** that
sentence with:

Because these fields are decisions rather than derivations, no generator ever
writes them: CI checks the committed file and nothing invents a disposition.

**Location:** section “Migration ledger”, after that paragraph. **Bloem adds:**

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

---

**Location:** end of the document. **Bloem adds:**

## Bloem native storage and generated contracts

Bloem keeps `/api/v2` separate from `/api/bloem/v1`. Native-storage
administration is a Bloem-owned protected platform/organization surface. Its route and wire
reference is [Native Storage Administration](../../../bloem-api-reference.md#native-storage-administration),
with all 34 scope-specific operations also generated into
[the native OpenAPI artifact](../../../../contracts/api/bloem/v1/openapi.json).
These are document-only chi declarations, not the Silo OpenAPI operation inventory
or additional runtime registrations. Its composition-dependent capability
schema never promotes artifact approval or synthetic provider tests to
`backend_verified`. The `internal/contractspec` member-shape lint (date, URL and
language formats) runs on `contracts/api/v2/openapi.json`, which includes Bloem's
`/api/v2` registrations; it does not check the native artifact.

Storage-source libraries use ordinary v1/v2 library operations, except where
locations combine: the v2 `createLibrary` takes exactly one of `paths` and
`storage_source`, while the scoped native `library-locations` routes combine local
paths and several selected source roots when native-storage capabilities advertise
`library_location_mixing`. Source roots retain opaque identities. The
[storage architecture](../../../architecture/bloem-native-storage.md) records
the publication, scanner and delivery readiness limits. Downloads and conversion
require their own supported delivery path; configuring a source does not grant them.

Generated OpenAPI/DTO agreement establishes a contract snapshot, not whether
an optional handler is mounted, an authenticated workflow passed against a
production server, or a native client uses it. Feature detection remains
required. The public plugin SDK version, private `StorageProvider` wire schema
and native-client API artifacts are separate compatibility surfaces; see
[protocol ownership](../../../architecture/bloem-native-storage.md#protocol).
