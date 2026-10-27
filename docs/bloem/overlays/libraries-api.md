# Bloem overlay: Library diagnostics API

**What this overlays.** Silo's [`docs/libraries-api.md`](../../libraries-api.md).
Bloem adds a section on storage-source libraries before “Unmatched items” and a
paragraph to “Accepted library work”. It replaces nothing. An *overlay* is a
Bloem-owned page that records Bloem's additions to, or replacements of,
sections of a Silo-owned document. The Silo file stays exactly as Silo wrote
it. See the [documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Two Bloem features change Silo's library operations:

- *native storage* lets a library read files from a storage plugin source
  instead of folders, which adds `storage_source` to `createLibrary`;
- entitlement bundles can still reference a library, so Bloem guards library
  deletion.

`storage_source` on `createLibrary` dates from `6c27ec35c` (2026-10-07) and the
deletion guard from `5436ed812` (2026-10-10). This overlay was created in the
Silo merge `a2bc7219c` (2026-10-18) and updated in `96249b876` (2026-10-25).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`internal/api/handlers/libraries_service.go`). Silo has not changed
this document since the previous review (Silo `6de6d5e5e`).

Index: [Bloem documentation](../README.md).

---

**Location:** before the section “Unmatched items”. **Bloem adds:**

## Storage sources

An ebook or admitted movie library can read its files from one storage source
instead of folders. `createLibrary` takes `storage_source` (a storage source key)
instead of `paths`; exactly one of the two is required. A missing pair answers
`422` at `body.paths`. Both together, another library type, or a source that is
unknown, disabled or unavailable to the library's owner answers `422` at
`body.storage_source`; a source that already backs a library answers
`409 storage_source_in_use`. The library resource reports `storage_source` only
for a library with no `paths` and exactly one storage location; mixed and
multi-location libraries are created, read and edited through the native
[library-locations routes](../../bloem-api-reference.md#library-locations-and-reading).
In `updateLibrary`, a storage library keeps its media type, and one without
filesystem paths refuses non-empty `paths`. The frozen `/api/v1` routes neither
return nor accept the member. See
[creating a library](../../architecture/bloem-native-storage.md#creating-a-library).

**Location:** section “Accepted library work”, after the paragraph beginning “`DELETE /api/v2/libraries/{id}` and library metadata refresh return `202`…”. **Bloem adds:**

Bloem refuses to delete a library that any entitlement bundle revision still
references: `DELETE /api/v2/libraries/{id}` answers `409` before it disables the
library or creates a job, and the deletion worker checks again under its folder
lock before its first batch. The final step otherwise follows Silo: a collection
that also covers another library moves to it, and series that survive elsewhere
recompute their membership, in the same guarded transaction that releases the
library's organization grants. See
[library deletion](../../architecture/bloem-lifecycle-safety.md#library-deletion).
