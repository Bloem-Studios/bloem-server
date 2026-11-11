# Bloem overlay: Native API contract: Huma and `/api/v2`

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`, and checked again on
> 2026-11-11, after Silo retired its v1 API. It lists what Bloem
> adds to or replaces in Silo's
> [`docs/architecture/api-contract.md`](../../../architecture/api-contract.md),
> which stays exactly as Silo wrote it. The section-by-section changes are in the
> [For developers](#for-developers) section.

Silo's page sets the rules for Silo's stable API, `/api/v2`, and for the
**migration ledger**, the file that records what happens to every older route as
it moves to `/api/v2`. Bloem follows those rules. It differs in two places: it
keeps the ledger up to date with a generator, and it publishes a second API of its
own, `/api/bloem/v2`, which Silo's page does not cover.

## What Bloem changes

- **The ledger's bookkeeping is generated.** Bloem adds routes to Silo's route
  groups, so almost every Silo merge shifts the route list. A generator copies the
  facts the route list already holds into the ledger. The decisions in the ledger
  are still made by people.
- **Bloem's own API is separate.** Features Silo does not have live under
  `/api/bloem/v2`, with their own API description. Bloem adds nothing to Silo's
  retired v1 API, and where Silo's `/api/v2` lacks something, Bloem adds a route of its
  own rather than changing Silo's.
- **A generated description is not proof.** Matching API descriptions show what
  the contract says, not that a route is switched on, that it works against a real
  server, or that an app uses it.

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| Migration ledger | `contracts/api/v2/migration.json`, checked by `make verify-migration-ledger` |
| Route list | `contracts/api/v2/route-inventory.json`, from `make route-inventory` |
| The ledger generator | `make migration-ledger` (`scripts/apiv2-ledger/refresh_ledger.py`) |
| Bloem's API description | `contracts/api/bloem/v2/openapi.json` |
| Feature detection | `feature_tokens` in `GET /api/bloem/v2/capabilities` |

## For developers

### Where this overlay came from

Bloem's own middleware changes the chain of almost every route, so its route
inventory moves on most merges. To keep the hand-curated migration ledger in step
without hand edits, Bloem added `make migration-ledger`
(`scripts/apiv2-ledger/refresh_ledger.py`, in `23b23f531`, 2026-09-16). Bloem first
documented these in the Silo file in September 2026 (`0f00b3eae`, 2026-09-01) and
moved the text here in `47ea7c889` (2026-09-23).

It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`Makefile`, `Makefile.bloem`, `scripts/apiv2-ledger/`,
`contracts/api/bloem/v2/openapi.json`). The earlier edition of this overlay
replaced Silo's whole description of the ledger tooling. Silo's section
assignment and consumer-evidence scripts still run in Bloem, so the replacement is
narrowed to the one sentence that differs. From `3e2d306b2` through `63ef34b23`
Silo changed the document six times (#3055, #3169, #3193, #3021, #3279, #3276);
none touches the migration ledger section, and all apply to Bloem as written.

### Section by section

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
byte, which is what `make verify-migration-ledger` checks (`refresh_ledger.py --check`, then the
section check) before it runs the Go gate. A seeded row
arrives `proposed` with no owner and no `v2` target: it is a placeholder for a decision, not a
decision, and the section PR still has to make it. Refreshing consumer evidence is a separate
operation and is not part of this target -- it needs the sibling client trees at a named commit
(see `scripts/apiv2-ledger/README.md`).

---

**Location:** end of the document. **Bloem adds:**

#### Bloem native storage and generated contracts

Bloem keeps `/api/v2` separate from `/api/bloem/v2`. Native-storage
administration is a Bloem-owned protected platform/organization surface. Its route and wire
reference is [Native Storage Administration](../../../bloem-api-reference.md#native-storage-administration),
with all 34 scope-specific operations also generated into
[the native OpenAPI artifact](../../../../contracts/api/bloem/v2/openapi.json).
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

#### Bloem and Silo's v1 retirement

Silo retired its v1 API on 2026-11-11 (the `410 Gone` tombstone with
`client_upgrade_required` described above; Silo #3200, merged into Bloem in
`26cf9b3e4`), and every Bloem route mounted beside it went too. Bloem's callers use
Silo's `/api/v2` for what Silo has and `/api/bloem/v2` for Bloem's own features; where
Silo's `/api/v2` lacks something Bloem needs, Bloem adds a native route. Nothing in Bloem
calls Silo's v1 API, and `make verify-no-v1` fails on a Bloem line that names a retired
path. Bloem's own API moved from v1 to `/api/bloem/v2` on the same day, with no alias; its
identifier is `"api": "bloem/v2"` and `bloem_api: ["v2"]`. It grows by adding fields and
routes, which clients detect through `feature_tokens` in `GET /api/bloem/v2/capabilities`,
and its version changes only for a breaking change to Bloem's own contract. See
[leaving Silo's v1 API](../../../architecture/bloem-leaving-silo-v1.md).

### Related pages

- [v1 scope overlay](v1-scope.md): what Bloem had on Silo's retired v1 API.
- [Writing Bloem documentation](../../documentation-workflow.md#api-and-swagger-sources):
  which source generates which contract.
- Index: [Bloem documentation](../../README.md).
