# Bloem overlay: Deterministic, Cross-Server `content_id`

Bloem additions and overrides for the upstream Silo document [`docs/architecture/deterministic-content-id.md`](../../../architecture/deterministic-content-id.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “Why `content_id`, not a file-level id”, after the paragraph beginning “`content_id` is the high-value target (35 FKs + ~25 unconstrained…”. **Bloem adds:**

### Music identity namespaces

Music uses two additional identifiers that are deliberately separate from the
provider-derived `content_id` scheme above:

- Artist IDs are normalized case-insensitive semantic hashes; letter case is
  presentation data and does not make a second logical artist.
- Album IDs are not semantic title hashes. A scan resolves an existing album
  by `(media_folder_id, canonical_root_path)` and otherwise generates a new
  item ID. Resolution and creation are serialized with a database transaction
  advisory lock on that tuple, so concurrent server processes reuse one ID.
  Renaming or moving an album root can therefore create a new album identity;
  no cross-server deterministic album contract exists yet.
- `music_tracks.id` identifies one physical track entry. It hashes the media
  folder ID, album ID, and cleaned slash-normalized path relative to the album
  root **without lowercasing the path**. This keeps the ID stable across scans
  while allowing `Track.flac` and `track.flac` to coexist on case-sensitive
  filesystems. The folder namespace prevents identical layouts in separate
  libraries from aliasing.

Older music track IDs hashed a lowercased absolute path. During a successful
scan, a legacy row is rewritten to the new ID only when its `media_file_id`
already owns that row. This ownership rule is the deterministic precedence for
a historical case collision: the one file representable by the legacy row
keeps its row history, and the other case-distinct file receives its own new
ID. The unowned collided legacy ID is not preserved; retaining both files and
stable identities takes precedence while the API is pre-lock.

Music catalog mutations share a folder-scoped PostgreSQL transaction advisory
lock (`bloem:music-folder-mutation:v1`). Ingest takes the shared mode so
independent albums can be written concurrently. Missing-file cleanup and
set-oriented present-state restoration take the exclusive mode; restoration
uses a different row-lock order and therefore must not overlap ingest.
Filesystem walking and probing happen before acquiring the lock. A vanished-file
event performs only its required exact-path stat recheck while holding it. When
an ingest also needs the album-root advisory lock, it always acquires the folder
lock first. Missing-file state changes, track deletion, and membership / orphan
reconciliation commit in one transaction; single-file events narrow that
reconciliation to the affected content item so a healthy-root event cannot purge
an intentionally preserved orphan under another root.
