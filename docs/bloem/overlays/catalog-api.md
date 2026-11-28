# Bloem overlay: Catalog API

This overlay covers Silo's [`docs/catalog-api.md`](../../catalog-api.md), which stays exactly as Silo wrote it.

Bloem serves Silo's catalog reads unchanged except for one thing in the section
"Library-scoped version lists".

## What Bloem changes

- **`getWatchState` follows `catalog.scope_versions_to_library`.** Silo leaves
  `GET /api/v2/watch/{id}` out of the setting, so it always lists every version the
  viewer may access. In Bloem, when the setting is on and the read names a
  `library_id`, it lists only the versions stored in that library, like
  `getCatalogItem` and the season and episode lists do. Without `library_id`, or with
  the setting off, it answers as Silo's does.

  Silo's Apple TV app picks the file to play from this read when the episode page
  has not already chosen one. With Silo's behavior it chose the first 1080p version,
  so an episode opened from an Alt Cuts library played the standard copy from the
  main TV library. Playback and watch-together selection are unchanged: a client that
  names a file still plays that file.

The code is a one-line seam in `internal/apiv2/watch.go`, listed in
`contracts/seams.txt` and tested by `internal/apiv2/bloem_watch_version_scope_test.go`.
