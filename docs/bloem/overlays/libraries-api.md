# Bloem overlay: Library diagnostics API

This overlay covers Silo's [`docs/libraries-api.md`](../../libraries-api.md), which stays exactly as Silo wrote it.

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
| Storage source on a new library | the native `library-locations` routes |
| Libraries that mix folders and sources | the native `library-locations` routes |
| A bundle that includes a library | an `entitlement_bundle_members` row; joining is the `media_folders_join_starting_set` trigger, leaving is `ON DELETE CASCADE` (`20261110123408_bloem_library_starting_set.sql`) |

## For developers

### Section by section

---

**Location:** before the section “Unmatched items”. **Bloem adds:**

#### Storage sources

An ebook or admitted movie library can read its files from storage sources
instead of folders. Such a library is created, read and edited through the native
[library-locations routes](../../bloem-api-reference.md#library-locations-and-reading);
Silo's `createLibrary` takes filesystem paths only, and the library resource carries
no storage field. Silo requires every library to have a path, so a library resource
with empty `paths` is one that reads storage sources.
In `updateLibrary`, a storage library keeps its media type, and one without
filesystem paths refuses non-empty `paths`. See
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
removes it from every bundle revision, so a library that a bundle revision names can
be deleted. See
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
