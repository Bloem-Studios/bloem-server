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

## Export and import

| Endpoint | Acknowledgment |
| --- | --- |
| `POST /api/v2/admin/catalog/export` | `200` with synchronous gzip bytes |
| `POST /api/v2/admin/catalog/export-jobs` | `202` after a queued export job is persisted |
| `POST /api/v2/admin/catalog/import` | `200` after the catalog import transaction commits |
| `POST /api/v2/admin/catalog/import-jobs` | `202` after a queued import job is persisted |
| `POST /api/v2/admin/catalog/export-jobs/{id}/publish` | `200` with a saved signed download URL |
| `GET /api/v2/admin/catalog/search/status` | Current catalog search runtime status |

Export requests use JSON `{}` for the entire catalog or `library_ids` containing
opaque string IDs. Direct export declares `application/gzip` as its success
representation; request it with an appropriate `Accept` header. A JSON-only
`Accept` is refused with `406`. Errors still use `application/problem+json`.
Other operations retain JSON negotiation. Large exports should use background
jobs, whose exporter writes through a temporary file before storage upload.

Import requests use JSON with exactly one of `local_path`, `export_job_id`,
`artifact_key`, or `remote_url`, plus `conflict_mode` (`skip_existing` or
`overwrite_existing`) and `path_rewrites` (an array of `from`/`to` pairs, possibly
empty). Null fields and incomplete rewrite pairs are rejected before execution.
Missing root rewrites produce a validation problem with `path_rewrite_required`
field errors that explain the affected source roots.

Direct import runs the existing catalog transaction and returns its committed
counts. If a response is lost, callers must inspect the catalog before deciding
whether to submit again. The web explicitly offers background execution or
"Import and wait"; it does not fall back between them after an error. All transfer
mutations disable automatic mutation and authentication retries.

Queued responses contain the existing typed administrator job and a `Location`
pointing to `GET /api/v2/admin/jobs/{id}`. An active job of the same kind produces
`409`; when available, `Location` identifies that active job. Persisting a job
acknowledges scheduling, not import/export completion or exactly-once execution.
The worker later opens the local path, downloads the remote URL, or reads the
storage object. Source bytes are not frozen at submission. Local paths must be
available on the worker that claims the job; remote content may change between
submission and execution.

The publish operation requires a completed export artifact. It saves a signed URL
with a seven-day signature lifetime and returns `job_id`, `url`, and `expires_at`.
It does not change the storage ACL. Repeating it returns the saved URL and original
expiry, even after expiry; it does not renew the link. Artifact retention or
removal can make the link unavailable before its signature expires.

Search status retains the existing runtime/provider/index information, uses UTC
millisecond instants, and represents `last_processed_event_id` as a string. Its
task links and media-type coverage groups are finite collections. These transfer
and status operations have no Apple, Android, or Jellyfin compatibility callers.

## Literary editions and matches

Literary administration uses acting-administrator authorization and the existing
literary work service. These routes remain registered when the service is absent
and return `503` until it is available.

| Endpoint | Result |
| --- | --- |
| `GET /api/v2/admin/literary-works/items/{content_id}/candidates` | Bounded ranked `candidates` |
| `POST /api/v2/admin/literary-works/link` | Selected `work_id` |
| `POST /api/v2/admin/literary-works/matches/confirm` | `status: "ok"` and `work_id` |
| `POST /api/v2/admin/literary-works/matches/ignore` | `status: "ok"` |
| `DELETE /api/v2/admin/literary-works/{work_id}/items/{content_id}` | Empty `204` |

Candidates accept `limit` from 1 to 100, default 20. This is a top-ranked result
set, not a paginated full inventory. The existing scorer filters candidates and
orders by descending score, breaking ties by target content ID. Each result
contains source and target content IDs, optional target work ID, score, link
source, and a string-valued evidence object. Empty evidence is `{}`.

Link accepts 1–100 nonempty `content_ids` and an optional `work_id`. Without a
work ID, the service reuses an existing linked work or creates one. Confirm and
ignore accept distinct `source_content_id` and `target_content_id`. Decisions
are attributed to the acting account's user ID, not a household profile ID.

Mutations return after their existing service calls finish. Confirmation first
links editions and then records the decision in a separate write; failure of
that write can leave the editions linked. There is no whole-operation transaction
or durable request replay receipt. All four mutations are non-retryable: after
an uncertain result, inspect current state before deciding whether to submit again.
These administration routes have no existing web, Apple, Android, or Jellyfin
compatibility callers. The viewer literary-work endpoint remains unchanged.
