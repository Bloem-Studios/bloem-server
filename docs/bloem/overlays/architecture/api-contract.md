# Bloem overlay: Native API contract: Huma and `/api/v2`

This overlay covers Silo's [`docs/architecture/api-contract.md`](../../../architecture/api-contract.md), which stays exactly as Silo wrote it.

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
  `/api/bloem/v2`, with their own API description. Where Silo's `/api/v2` lacks
  something, Bloem adds a route of its own rather than changing Silo's.
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

**Location:** section “Credential continuity”, after the paragraph beginning “The playback
delivery routes (`getPlaybackMedia`, …) accept the plan's signed stream reference `st`”
(Silo #4060). **Bloem adds:**

On Bloem, the stream-token gate reads the same `st` first. A token it accepts (valid
signature, not expired, naming the session in the path) authorizes delivery of that
session's bytes, and viewer access then resolves the token's own account and profile;
the attempt and login-session checks above do not run for it. Any other request with
`st` goes through the reference check as described. See
[tenant identity in Silo's own gate chains](../../../architecture/bloem-upstream-adapters.md#tenant-identity-in-silos-own-gate-chains).

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

Storage-source libraries use ordinary `/api/v2` library operations, except where
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

#### Bloem's API versions

Bloem's callers use Silo's `/api/v2` for what Silo has and `/api/bloem/v2` for Bloem's own
features; where Silo's `/api/v2` lacks something Bloem needs, Bloem adds a native route. Bloem's
API identifier is `"api": "bloem/v2"` and `bloem_api: ["v2"]`. It grows by adding fields and
routes, which clients detect through `feature_tokens` in `GET /api/bloem/v2/capabilities`,
and its version changes only for a breaking change to Bloem's own contract.

### Related pages

- [Writing Bloem documentation](../../documentation-workflow.md#api-and-swagger-sources):
  which source generates which contract.
- Index: [Bloem documentation](../../README.md).
