# Catalog administration API

Catalog source discovery uses acting-administrator authorization. These endpoints
expose server paths and storage keys intentionally for catalog administration.
Ordinary profiles cannot use them.

| Endpoint | Result |
| --- | --- |
| `GET /api/v2/admin/catalog/import-sources` | One storage page, filtered to catalog seed bundles |
| `GET /api/v2/admin/catalog/local-import-sources` | Local catalog seed files, ordered by path |
| `GET /api/v2/admin/filesystem/browse` | Subdirectories with resolved `path` and `parent` |

All three accept `limit` (1–200, default 50) and an opaque `cursor`. Results have
`items` and `page`, including `has_more` and an optional `next_cursor`. Cursors are
bound to the operation and caller; filesystem cursors also bind the requested path
and name prefix. Changing those filters starts a new listing.

Storage discovery makes one bounded S3 listing request per API request. A page can
contain no matching bundles and still have `has_more: true`; clients must retain
its continuation cursor. Storage order follows the provider's listing order,
not modification time. Local listings use ascending path order. The web offers
explicit continuation controls and does not drain every page in the background.

Filesystem browsing accepts `path` (an absolute path, defaulting to the filesystem
root) and optional `name_prefix` (case-insensitive). Prefix filtering happens
before pagination so autocomplete can find folders outside the first unfiltered
page. Directory symlinks are followed; broken links and ordinary files are omitted.
Local seed discovery accepts regular `.json.gz` files and links to those files.

Local directory enumeration reads fixed-size batches and retains only a bounded
page of candidates. It still scans the directory to establish ordering. Results
reflect the responding server's filesystem and are not a snapshot across pages;
concurrent additions and removals may change later pages. A missing configured
local seed directory returns an empty listing; a missing explicitly browsed
directory returns not found.

These are web administration utilities. The Apple and Android clients have no
callers for them, and the Jellyfin compatibility surface has no equivalent catalog
seed or administrator filesystem operation.
