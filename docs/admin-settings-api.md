# Administrator settings inspection

The following native v2 operations require an acting administrator and return
no-store responses:

- `GET /api/v2/admin/settings`: stored settings, excluding secret values and
  machine-managed keys.
- `GET /api/v2/admin/settings/effective`: active settings with the runtime's
  defaults applied, using the same redaction policy as stored settings.
- `GET /api/v2/admin/settings/restart-keys`: the compiled `keys` and `prefixes`
  whose changes need a server restart.
- `GET /api/v2/admin/settings/sensitive-status`: configured secret key names and
  `managed_by_env` names. Values are never returned. Both fields are arrays,
  including when empty.

Stored and effective settings are string dictionaries keyed by the server
settings registry. An empty dictionary is `{}`. Inspection copies stored values
before redaction so a cached settings map remains intact. Missing settings storage
returns `503 dependency_unavailable`; other storage failures return a generic
problem without database error details. Restart metadata is available without
settings storage.

The web settings pages use the v2 effective settings, restart metadata, and
sensitive-status operations. Writes remain on their existing bridge operations
until the corresponding mutation contracts migrate. The migration inventory lists
no Apple or Android consumers for these inspection operations. Jellyfin
compatibility does not expose these administrator settings contracts.

`POST /api/v2/admin/settings/check/{kind}` performs one synchronous connection
check against the submitted `values` and `dirty_keys`, merged with stored settings.
Supported kinds are `s3_public`, `s3_operational`, `s3_private`, `redis`,
`recommendations_embedding`, `ai_chat`, `ai_transcription`, `meilisearch`, and
`mdblist`. Existing endpoint-change protection for stored AI credentials applies.
Provider failures return `success: false` with a generic message that excludes
provider error bodies and credentials. Invalid kinds/configuration return `422`.

Checks can write temporary storage objects or incur provider charges. They return
a synchronous result, not a persisted job. The web sends each user-triggered check
once and disables mutation retries; a lost response must not trigger automatic
replay. This corrects the inventory's earlier assumption that every check was
read-only. Demo mode blocks this operation.

## Request queue scope

`GET /api/v2/admin/requests/capabilities` advertises `global_requests: true` when
request administration supports the shared queue option. Acting administrators
can read and set the optional boolean `global_requests` through
`GET`/`PUT /api/v2/admin/request-settings`, using the existing ETag/If-Match guard.
The default is false: duplicate detection is scoped to the requesting account's
organization. True shares active-title duplicate detection across organizations.
Omitting the field on PUT preserves its current value for older clients.

Switching to global mode returns `422 validation_failed` if organizations have
overlapping active requests for a title. The rejected write leaves both settings
and requests unchanged. Resolve those active requests before retrying. Successful
mode changes and request scope updates are atomic. Failed-request cleanup remains
organization-scoped in both modes. Global status does not expose another user's
request ID or requester identity; library access remains independently enforced.

The existing web request-settings form exposes the toggle. The inspected Apple
and Android v3 clients have no consumers of this administrator settings operation;
no native wire change is required. The setting does not add a compatibility API.
