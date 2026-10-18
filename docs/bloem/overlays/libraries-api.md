# Bloem overlay: Library diagnostics API

Bloem additions and overrides for the upstream Silo document [`docs/libraries-api.md`](../../libraries-api.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** before the section “Unmatched items”. **Bloem adds:**

## Storage sources

An `ebooks` library can read its books from a storage source instead of
folders. `createLibrary` takes `storage_source` (a storage source key) instead
of `paths`; exactly one of the two is required, and a missing pair answers `422`
at `body.paths`. A source that is unknown, disabled or unavailable to the
library's owner, or a non-ebook library, answers `422` at `body.storage_source`;
a source that already backs a library answers `409 storage_source_in_use`. The
library resource reports `storage_source` and an empty `paths`. A storage
library keeps its source: `updateLibrary` refuses `paths` and a change to
another kind. The frozen `/api/v1` routes neither return nor accept the member.
See the [storage architecture](../../architecture/bloem-native-storage.md).
