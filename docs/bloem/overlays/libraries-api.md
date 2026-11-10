# Bloem overlay: Library diagnostics API

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`; the deletion rule
> was updated on 2026-11-10. It lists what Bloem
> adds to Silo's [`docs/libraries-api.md`](../../libraries-api.md), which stays
> exactly as Silo wrote it. Bloem replaces nothing there. The additions are in the
> [For developers](#for-developers) section.

Silo's page explains the library routes that report and repair a library's
contents. Bloem serves them unchanged and adds two rules that come from Bloem's own
features.

## What Bloem changes

- **A library can read from a storage source.** With Bloem's **native storage**,
  an ebook or movie library can read its files from a **storage source**, such as
  an S3 bucket behind an approved storage plugin, instead of folders on the server.
  Creating a library therefore takes either folders or one storage source.
- **A new library joins the starting set, and a deleted one leaves it.** An
  **entitlement bundle** is a set of libraries and plugins that organizations are
  granted together, such as the platform's **starting set** for each new
  organization. A platform library created through these routes joins the starting
  set, and deleting a library takes it out of every bundle in the same step that
  removes its grants.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Storage source on a new library | `storage_source` on v2 `createLibrary`, instead of `paths` |
| Libraries that mix folders and sources | the native `library-locations` routes |
| A bundle that includes a library | an `entitlement_bundle_members` row; joining is the `media_folders_join_starting_set` trigger, leaving is `ON DELETE CASCADE` (`20261110123408_bloem_library_starting_set.sql`) |

## For developers

### Where this overlay came from

`storage_source` on `createLibrary` dates from `6c27ec35c` (2026-10-07) and the
deletion guard from `5436ed812` (2026-10-10). This overlay was created in the Silo
merge `a2bc7219c` (2026-10-18) and updated in `96249b876` (2026-10-25). On 2026-11-10
deletion stopped refusing libraries a bundle includes and new libraries started
joining the starting set (`20261110123408`). It was
reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at `e852f92bf`
(`internal/api/handlers/libraries_service.go`). From `3e2d306b2` through
`63ef34b23` Silo changed the document once, in `73a6ae6a3` (#3114, 2026-10-27),
which keeps titles that are in no library out of administrator listings; that
applies to Bloem unchanged, and both passages this overlay points at are
unchanged.

### Section by section

---

**Location:** before the section “Unmatched items”. **Bloem adds:**

#### Storage sources

An ebook or admitted movie library can read its files from one storage source
instead of folders. `createLibrary` takes `storage_source` (a storage source key)
instead of `paths`; exactly one of the two is required. A missing pair answers
`422` at `body.paths`. Both together, another library type, or a source that is
unknown, disabled or unavailable to the library's owner answers `422` at
`body.storage_source`; a source that already backs a library answers
`409 storage_source_in_use`. A server without storage-source support answers
`503 storage_unavailable`. The library resource reports `storage_source` only
for a library with no `paths` and exactly one storage location; mixed and
multi-location libraries are created, read and edited through the native
[library-locations routes](../../bloem-api-reference.md#library-locations-and-reading).
In `updateLibrary`, a storage library keeps its media type, and one without
filesystem paths refuses non-empty `paths`. The frozen `/api/v1` routes neither
return nor accept the member. See
[creating a library](../../architecture/bloem-native-storage.md#creating-a-library).

---

**Location:** section “Accepted library work”, after the paragraph beginning
“`DELETE /api/v2/libraries/{id}` and library metadata refresh return `202`…”.
**Bloem adds:**

Bloem's deletion worker holds a folder lock from before its first batch until the
library is gone, so nothing can add the library to an entitlement bundle meanwhile.
The final step otherwise follows Silo: a collection that also covers another library
moves to it, and series that survive elsewhere recompute their membership, in the
same guarded transaction that releases the library's organization grants and
removes it from every bundle revision. Before 2026-11-10,
`DELETE /api/v2/libraries/{id}` answered `409` ("Library is retained by an
entitlement bundle") for a library any bundle revision named. See
[library deletion](../../architecture/bloem-lifecycle-safety.md#library-deletion).

`createLibrary` adds a platform-owned library to the active revision of the
starting set when its transaction commits; one created for an organization through
native storage stays out. See
[the starting set](../../architecture/resource-tenancy-foundation.md#the-starting-set).

### Related pages

- [Native storage](../../architecture/bloem-native-storage.md): sources, mixed
  locations and their readiness limits.
- [How organizations work](../../architecture/bloem-organizations.md): grants and
  the starting set.
- [Admin API overlay](admin-api.md): native storage administration and the
  library operations Bloem refuses on native libraries.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).
