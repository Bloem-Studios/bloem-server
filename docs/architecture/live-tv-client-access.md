# Live TV client access

The separate `watch_live_tv` permission controls the Bloem app-facing Live TV
surface. It is not included in default user permissions and does not depend on
movie, series or book library grants. Admin account creation/editing exposes the
grant; access groups and entitlement templates expose the existing permission
mask. A group mask permits or restricts an account grant, rather than granting
permissions by itself.

Both viewer resolvers derive `LiveTVAllowed` from the effective permission set.
The primary profile of an admin account has an implicit grant; other household
profiles need an explicit effective grant. Existing account/group persistence
and revision invalidation apply without a schema migration.

`live_tv_access_v1` in the native capability document identifies this contract.
An authenticated, selected profile can request
`GET /api/bloem/v1/livetv/capability`:

- `supported`: the server implements this contract.
- `allowed`: the current viewer has the separate Live TV grant.
- `available`: the viewer is allowed and at least one channel is enabled.
- `heartbeat_interval_seconds`: 30.

The response is not cacheable. A database failure is an error rather than a
false empty state; denied viewers do not trigger a channel lookup. Native
clients should distinguish denial, unsupported servers and network errors.
The existing channel, guide, session and DVR endpoints remain under `/api/v1`.
Their app-facing route group enforces the grant. Owned session release remains
available after revocation so the viewer can free a tuner.

Session-bound signed delivery credentials retain their existing lifetime and
ownership semantics. They authorize only the existing delivery, never channel
browsing, new tuning, recording or session heartbeat. This change does not
provide immediate revocation of bytes already authorized by a signed session.

The DTO registry includes named channel/guide/session/DVR envelopes and request
models. Channel DTOs use the actual redacted response projection: `stream_url`
is empty, never the private tuner address. Do not bypass this projection when
adding a new channel response.

## Integration status

This is the first server slice, not a deployment-ready completion of Live TV
permissions across every protocol. The separate Jellyfin-compatible and internal
compatibility gateway Live TV adapters need equivalent permission enforcement
before deploying a server-wide access guarantee. The web viewer navigation and
both v3 clients still need to consume the capability and use the generated DTOs.
The native permission controls and app-facing routes can be tested independently
while those integrations are completed. Do not infer adapter coverage from the
shared service alone.
