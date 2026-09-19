# Xtream live TV providers

Bloem consumes authenticated Xtream **live channels** and the provider's XMLTV
programme guide. It does not expose an Xtream server API, import VOD or series,
or accept arbitrary remote media URLs. HDHomeRun-compatible tuners remain supported.

## Setup and capability

`GET /api/bloem/v1/livetv/capability` adds `xtream_supported`. This reports installed
protocol support, not working credentials, encryption readiness, playback success
or a viewer grant. Existing `allowed` and `available` retain their meanings.

An acting administrator with the account's selected primary profile and any required
PIN verification creates a provider with:

```http
POST /api/bloem/v1/livetv/tuners/xtream
Content-Type: application/json
```

The JSON fields are `url`, `username`, `password`, optional public `name`, and
optional `max_connections` (1–64; omitted or zero uses one). Do not embed credentials
in `url`. Success is `201` with the ordinary redacted tuner DTO; `type` is `xtream`
and `model` carries the public name. The separate endpoint prevents an older
server from interpreting this request as unauthenticated HDHomeRun setup. Generic
`POST /livetv/tuners` refuses the Xtream kind.

Creation authenticates the account and imports a nonempty live lineup before
atomically saving the tuner, channels and encrypted credentials. The local budget
is clamped to a positive maximum reported by the provider. Provider-unlimited does
not remove Bloem's local budget. The same username at the same canonical base URL
cannot be configured twice; aliases for the same external account are not detected.
There is a server-wide limit of 64 configured Xtream providers.

The embedded Tuners form keeps credentials outside React state, query/mutation
caches and browser storage. Submission clears the inputs immediately. Requests
capture account, origin, profile and PIN authority, do not queue or automatically
retry, and abort on unmount or authority changes. An uncertain result blocks another
creation across component remounts until an explicit provider reload succeeds.
This is reconciliation, not exactly-once execution across tabs or page reloads.

## Credentials and network boundary

- The existing `SECRET_KEY` cipher encrypts both credentials together. Authenticated
  encryption binds the envelope to the tuner ID and canonical base URL. Missing
  encryption wiring fails closed before contacting the provider.
- Only fixed `player_api.php`, `get_live_streams`, `xmltv.php` and live `.ts` requests
  are made. All redirects are refused, including redirects to another path on the
  same host. Provider `direct_source` and artwork URLs are not adopted.
- Validation and dial-time checks reject loopback, unspecified addresses and the
  reviewed cloud metadata destinations while retaining ordinary LAN tuner support.
- Credentials necessarily appear in the provider's authenticated HTTP requests,
  but not in public DTOs, stored channel URLs, application diagnostics or FFmpeg
  arguments. Prefer HTTPS: an HTTP provider receives credentials without TLS.
- Channels retain opaque server-only source references. Raw native delivery,
  Jellyfin delivery, HLS and DVR resolve credentials server-side under their
  existing viewer, profile, session and ownership checks. A delivery ticket does
  not authorize provider administration.
- Playback requires direct MPEG-TS. The server checks the initial TS packet framing;
  this is not a codec/decoding guarantee. Playlist, text and JSON responses are
  refused. FFmpeg receives `-protocol_whitelist pipe -f mpegts -i pipe:0`, never an
  authenticated network URL. IPTV does not assume an OTA MPEG-2 hardware decoder.

Each physical upstream stream claims a durable, unpredictable-ID-fenced connection
lease. Raw proxying, HLS encoders and recordings share the provider budget across
replicas. Leases expire after 60 seconds and renew every 15 seconds with bounded
DB operations and conservative local expiry checks. Failed renewal stops that
input; an expired owner cannot revive or release its replacement's lease. This
bounds Bloem's admissions, not a provider's accounting of disconnected TCP sessions
or connections opened by other applications. It does not provide seamless failover.

The process input copier is server-owned. Encoder exit can be reaped while an
upstream read is blocked; cancellation and cleanup close the source and wait for
the copier. HLS startup observes the requesting context through readiness, then
uses its existing session-owned lifetime. DVR retains its recording context and
scheduled stop.

## Guide and rescan behavior

Add a guide through the existing guide-source endpoint with `type: "xtream"` and
`config: {"tuner_id": "<configured-provider-id>"}`. Only that provider ID is stored
in public guide configuration. The Guide tab selects existing providers and never
asks for another password or an arbitrary guide URL. Use **Sync** after creation.
One Xtream guide per provider and the existing maximum of three enabled guide
sources are enforced transactionally across replicas. The guide form captures
profile/PIN authority and confirms creation with a fresh source read. A lost response
or failed readback blocks further creation across remounts until an explicit reload
succeeds; background refreshes do not clear that guard.

XMLTV is limited to 64 MiB, one million input programme entries and 250,000 retained
programmes. Import keeps overlapping listings from six hours ago through the next
48 hours, restricted to enabled channels of the selected provider. Channel EPG IDs
match first; the provider stream ID is the fallback. Programme timestamps require
seconds, with an optional numeric timezone (UTC when omitted). `xmltv_ns` episode
numbers become one-based. Artwork URLs are ignored. Simple external DTD declarations
are accepted without fetching them; internal entity declarations are refused.

A complete, valid, nonempty matching feed is required before publication. Source
MVCC ownership fences overlapping refreshes, configuration changes (including ABA)
and late status updates without holding a database connection during downloads.
Publication rechecks and locks the captured channel mappings. Guide rows and the
ready status commit together. Stable source/channel/start identities and in-place
updates preserve ordinary recording links and captured schedules; a malformed or
stale refresh does not erase the previous guide.

Rescans retain stable channel IDs, overrides and mappings. Missing channels are
disabled rather than cascade-deleted; returning channels stay disabled for review.
An empty rescan reports failure and keeps the old lineup. A lower reported provider
limit reduces admission capacity; it does not forcibly terminate existing recordings.

Provider removal refuses active physical leases and requires confirmation in the
web UI. It deletes encrypted credentials, linked guide configuration, channels and
their DVR entries. It does **not** delete recorded files. Credential/base-URL edits
and connection-budget increases are not exposed: removing and re-adding a provider
is destructive to those database entries. Deleting/recreating guide sources or
using overlapping sources retains the inherited recurring-DVR duplicate-scheduling
limitations; normal refresh stability does not resolve every DVR lifecycle case.
Automatic recording import and enforced `keep_last` remain unsupported.

## Deployment and acceptance

Commit `418a18b7d` was deployed on September 19, 2026, with both the policy-array
and Xtream migrations applied after a scoped restored-backup rehearsal. Health,
readiness and unauthenticated login smoke checks passed. No provider was configured;
these checks do not establish provider or decoded-media acceptance. The
[deployment record](../operations/2026-09-19-xtream-deployment.md) records the exact
revision, preserved-data checks, rollback limits and explicitly approved scenario
CI-timeout exception. That CI run remains failed.

Migration `20260919133349_bloem_xtream_sources.sql` adds the credential and lease
tables. Upgrade the complete API/worker fleet before configuring providers; older
nodes do not understand these protected sources. Back up the existing encryption
key with the protected configuration: retaining the database alone is insufficient.
Image rollback retains additive schema and data. Do not administer Xtream sources
through older binaries. Explicit schema Down requires quiesced writers and refuses
any remaining Xtream tuner, guide, credential or lease state, including expired
leases and orphan guide configuration.

Automated coverage includes protocol fixtures, real disposable PostgreSQL storage,
connection/guide admission across pools, source and channel publication fencing,
recording-link preservation, migration round trips/refusal, credential/guide-form
reconciliation, mounted native authority gates and fixture-encoder cleanup. This is not real-provider, physical-tuner,
Safari, decoded-media or multi-replica owner-loss acceptance. Apple/Android provider
administration is a client follow-up, not an implemented part of this server change.
