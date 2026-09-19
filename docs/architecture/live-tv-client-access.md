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
- `xtream_supported`: this build supports authenticated Xtream live providers; absent
  on older builds. This does not replace the viewer grant or prove provider readiness.
- `allowed`: the current viewer has the separate Live TV grant.
- `available`: the viewer is allowed and at least one channel is enabled.
- `heartbeat_interval_seconds`: 30.

The response is not cacheable. A database failure is an error rather than a
false empty state; denied viewers do not trigger a channel lookup. Native
clients should distinguish denial, unsupported servers and network errors.
Channel, guide, session, DVR and tuner-administration endpoints are mounted under
`/api/bloem/v1/livetv`, not `/api/v1/livetv` or Silo's `/api/v2`. The embedded web
uses this native prefix for reads, mutations, lease renewal and teardown.
The app-facing route group enforces the grant. Owned session release remains
available after revocation so the viewer can free a tuner.

The native constructor returns native media URLs and supplies the signing secret.
Only `GET /api/bloem/v1/livetv/live-hls/{id}/index.m3u8` and its segment paths accept
headerless `st` delivery, with the signed `bloem_livetv_hls` purpose, delivery ID,
expiry and freshly resolved tenant/profile grant. The raw MPEG-TS proxy retains normal
account/profile authentication. Tokens cannot authorize operational routes or broaden
direct-profile admission. The native household checker reads the authoritative profile
store before allowing a primary admin to manage another viewer's sessions or DVR entries.

Session-bound signed delivery credentials retain their existing lifetime and
ownership semantics. They authorize only the existing delivery, never channel
browsing, new tuning, recording or session heartbeat. This change does not
provide immediate revocation of bytes already authorized by a signed session.

The DTO registry includes named channel/guide/session/DVR envelopes and request
models. Channel DTOs use the actual redacted response projection: `stream_url`
is empty, never the private tuner address. Do not bypass this projection when
adding a new channel response.

## Xtream providers

The Tuners tab supports encrypted, live-only Xtream provider accounts; the Guide tab
can attach their bounded XMLTV feed. The dedicated native creation endpoint, shared
physical-connection limits, pipe-only encoder input, destructive removal semantics
and deployment prerequisites are documented in [Xtream live TV providers](xtream-live-tv.md).

## Integration status

The Jellyfin-compatible adapter resolves the same current account/profile scope
for guide, channel, DVR, tuning, playback negotiation and stream delivery. Its
user policy and synthetic Live TV library are viewer-specific. Missing resolver
wiring and resolver failures deny access. Closing an owned stream remains
available after revocation. Existing connections are not forcibly terminated.

The internal compatibility gateway has no production Live TV service wired in
this checkout; those routes remain unavailable. Any future service adapter must
resolve the subject's effective Live TV grant before returning data or tuning.
Do not infer adapter coverage from the shared service alone.

The web viewer consumes the native capability for its sidebar and guide/watch
route gates. Profile-bound requests discard late results after a profile switch.
The live watch route negotiates browser codecs, renews the tuner lease, and
releases it on navigation, page teardown, lost access or fatal playback errors.
Tune requests are never automatically retried; late tune responses are released
even if the viewer has already left. The existing browser Live TV player is
used. The native-HLS fallback requires browser HLS support and a server-issued `st`
stream ticket. Account/profile credentials are never placed in media URLs, and HLS
requests cannot forward authorization to a foreign origin. Native fallback has automated
coverage but still needs real Safari/device playback acceptance.

The web exposes manual channel/time recordings and recurring rules (title substring,
channel/new-only filters, or an exact guide `series_id`). Mutations capture profile
authority, are not queued offline or replayed after uncertain failure, and await
authority-bound readback after success or uncertain failure before another action.
Manual drafts retain their original captured authority. Failed recording readback
keeps manual, guide and cancel actions blocked until an explicit Reload recordings
action succeeds, including active filtered lists; background refresh alone cannot
clear the block. Older list reads are cancelled so they cannot replace the reconciled
result. Recovery does not resend the write. The guard lives in the current QueryClient,
so it does not provide exactly-once execution across reloads, tabs or other clients.
Rebuilt browser acceptance passed real disposable scheduling and cancellation with
readback, including committed writes whose browser responses were dropped. Nine
deliberately injected GET `503`s verified that failed reads and failed explicit reload
kept actions blocked; successful explicit reload cleared the block and required a
fresh manual draft. The run observed two POSTs and two DELETEs, cancelled both created
rows, preserved captured authority and reported no browser runtime errors. This
validates reconciliation against the real backend, not exactly-once execution or
actual tuner, recording or Safari playback.

Rule removal requires confirmation; it does not cancel existing scheduled recordings.
There is no rule-update operation, and stored `keep_last` does not enforce retention.
Completed recordings link to their library item only when `library_item_id` is present.
The recorder does not automatically import recordings or populate that link; the UI
explains that an administrator must scan a recording folder through a library.

Both v3 clients still need capability integration and the generated DTOs.
Native session/DVR ownership overrides require the primary admin profile;
child, unknown and unresolvable profiles keep owner-only access. No production deployment is certified by this work.

Normal URL validation and dial-time SSRF guards reject loopback tuner destinations.
Use a permitted isolated network for media acceptance; never disable those guards
to make a local fixture pass. An offline transport-stream decode does not prove
server-mediated playback. Actual tuner, Safari, prolonged playback and multi-replica
owner-loss checks remain in the [completion handoff](bloem-web-completion-handoff.md).
