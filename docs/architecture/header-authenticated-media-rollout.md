# Header-Authenticated Media Client Rollout

Status: approved design, not yet implemented

This document defines the coordinated server, Android, and Apple rollout of
the protocol-v3 feature tokens `software_video_decode_v1`,
`header_authenticated_media_v1`, and `authorized_media_origins_v1`.

The existing protocol contract remains authoritative for wire shapes and
attempt-sticky behavior. This document adds the deployment-readiness,
client-ownership, security, recovery, and rollout rules needed before the
first-party Vondel clients advertise those features in production.

## Goals

- Let each playback device report its own runtime decode capabilities instead
  of relying on model names, platform defaults, or handwritten codec tables.
- Use the same immutable capability evidence for an attempt's initial plan,
  replans, and Watch Together playback.
- Allow tokenless, header-authenticated media only when the deployment can
  route it safely and reliably.
- Prevent credentials from leaking through redirects, untrusted origins,
  persisted plans, telemetry, or Cast handoff.
- Preserve a safe, server-controlled rollback to the legacy signed-URL mode
  without requiring a client release.

## Non-goals

- Moving playback-session state into shared storage.
- Sending a phone or tablet's capabilities or bearer token to a Cast receiver.
- Inferring capabilities from a device model, operating-system version, or
  marketing family.
- Enabling header-authenticated media automatically merely because a server
  binary implements the protocol.
- Changing the attempt-sticky semantics already defined by playback protocol
  v3.

## Terminology

- **Support token:** a capability token indicating that the server binary
  implements a protocol behavior.
- **Readiness token:** a capability token indicating that this deployment is
  currently configured to use that behavior safely.
- **Activation snapshot:** the immutable runtime capability snapshot captured
  when an authenticated playback activation opens.
- **Playback attempt:** the lifetime identified by one playback attempt ID and
  its server-issued attempt key.
- **Legacy media:** the existing signed-URL transport carrying an opaque stream
  token.
- **Header-authenticated media:** credential-free media URLs fetched with the
  current API `Authorization` header.

## Architecture

### Server support and deployment readiness

The server continues to advertise the implementation support tokens
`header_authenticated_media_v1`, `authorized_media_origins_v1`, and
`software_video_decode_v1` where protocol-v3 support is described.

Header-authenticated media additionally requires the dynamic readiness token:

```text
header_authenticated_media_ready_v1
```

The server advertises this readiness token only when the admin setting
`playback.header_authenticated_media_mode` is `single_or_affine`. The setting
defaults to `disabled` and accepts only:

- `disabled`: do not negotiate header-authenticated media;
- `single_or_affine`: the operator declares that media requests reach the
  playback-session owner through a single API replica or verified affinity,
  and that configured proxy origins can validate the same live login session.

The readiness token is intentionally separate from the support token. A
server binary can understand the protocol while a particular deployment is
unsafe for tokenless sessions.

The capability document is the sole authority for readiness. Clients do not
infer readiness from server version, replica count, proxy availability, URL
shape, or a previously successful attempt.

If a stale client sends `header_authenticated_media_v1` while readiness is
disabled, the start boundary ignores both header-authenticated feature tokens
and creates a legacy attempt. It must not create a partly tokenless attempt and
must not fail an otherwise playable request. A replan cannot change the
security mode chosen at start.

`authorized_media_origins_v1` is accepted only when all of these are true:

1. `header_authenticated_media_v1` is accepted for the attempt;
2. deployment readiness is enabled;
3. the server can publish a validated absolute media origin for the selected
   route.

Otherwise the plan remains on the authenticated API origin. The feature is a
permission to use a designated origin, not a promise that every plan will.

### Client feature selection

The client constructs one immutable feature set from the server capability
document and the activation snapshot. That exact set is used for start and
every replan in the attempt.

The selection rules are:

- include `software_video_decode_v1` only when the runtime snapshot contains a
  supported, bounded video decode entry whose `hardware` value is `false`;
- include `header_authenticated_media_v1` only when the server advertises both
  the support token and `header_authenticated_media_ready_v1`, and the local
  playback engine has the required authenticated resource-loading path;
- include `authorized_media_origins_v1` only with
  `header_authenticated_media_v1` and only when the client can enforce the
  authorized-origin rules below.

Unknown runtime evidence remains unknown. Empty codec, container, transport,
or decoder evidence is never filled from a static fallback.

The feature set is owned by the activation's playback bundle rather than by a
screen, request serializer, replan helper, or Watch Together coordinator.
Watch Together receives the already-bound plan source and remains unaware of
capability negotiation.

### Android ownership

Android's authenticated activation owns one frozen
`PlaybackCapabilitySnapshot`, its feature set, the start plan source, the
replan client, and the capability evaluator. Phone, TV, detail playback, and
Watch Together reuse that bundle for the activation generation.

The HTTP start and replan serializers project the same feature set. They do
not independently probe the device or construct defaults.

Media3 uses one authenticated resolving data-source policy for progressive
media, HLS manifests and segments, subtitles, and font artifacts. The policy
receives the API origin and the exact selected media origin from the accepted
plan.

### Apple ownership

Apple's authenticated composition owns one frozen activation snapshot, its
feature set, the playback plan source, and the capability evaluator. Initial
planning, replanning, downloads eligibility, and Watch Together consume that
same activation generation.

AVFoundation media loading uses an owned resource-loading boundary capable of
authorizing and validating every manifest, segment, subtitle, and font
request. Supplying `AVURLAssetHTTPHeaderFieldsKey` alone is insufficient as the
security boundary because it cannot prove redirect and derived-request
containment.

An audio-route or display change may open a new activation generation. It does
not mutate the feature set of an active playback attempt.

### Receiver separation

Cast and other remote receivers are separate playback devices. A sender sends
only stable content, episode, file, and resume identity. It never sends the
local activation snapshot or bearer token.

A receiver may use these features only after reporting its own capability
snapshot and establishing its own authenticated session. In the absence of
that report, receiver playback fails closed for these features and retains its
existing supported transport.

## Authorized-origin security model

For a header-authenticated attempt, the client forms an immutable set of
authorized media origins containing:

- the authenticated API origin; and
- at most the exact absolute media origin selected by the accepted plan.

Origin comparison includes scheme, host, and effective port. Internationalized
host names are compared in canonical ASCII form. User information, fragments,
opaque URLs, malformed ports, and non-HTTP schemes are rejected. HTTPS may not
redirect to HTTP.

Each request must remain on its initiating authorized origin. Redirects are
followed only when the destination has the same canonical origin as the
initiating request. A redirect never gains authorization merely because its
destination is the other authorized origin. The bearer header is stripped
before any rejected redirect can be followed.

Relative child URLs resolve against their parent manifest or artifact URL.
The plan, not the client, is the only authority allowed to introduce the
second media origin.

The current API bearer credential is attached to:

- the initial file or manifest request;
- every HLS child manifest and media segment;
- subtitle artifacts and inventories;
- subtitle font bundles; and
- range, retry, and reload requests for the same authorized resource.

Plans and persisted playback state do not contain the bearer token. The server
does not echo it in `stream.headers`. Logs and telemetry may record an origin
classification or hashed attempt identifier, but never the header, token,
private media URL, or signed URL.

## Failure and recovery

Negotiated security and software-decode features are fixed for a playback
attempt. A replan neither adds nor removes them, even if a new runtime snapshot
becomes available.

When an authorized media request returns an authentication failure:

1. the client verifies that the attempt and activation generation are still
   current;
2. it performs at most one credential refresh for the current authentication
   generation;
3. it rebuilds or reloads the same authorized media resource with the new
   header and resumes from the current source position;
4. concurrent failures share that refresh rather than starting parallel
   refreshes.

If refresh establishes that the login session no longer exists, or a
header-authenticated session is missing after an API restart, the client stops
the old attempt and starts exactly one fresh legacy attempt without
`header_authenticated_media_v1` or `authorized_media_origins_v1`. It preserves
the selected content, file, tracks, room identity where applicable, and source
position. The fresh attempt receives a new attempt ID and may not fall back a
second time.

Legacy fallback is not triggered by:

- cancellation or stale activation ownership;
- ordinary connectivity or timeout failures;
- decoder, container, or format failures;
- a generic server error;
- an untrusted redirect or origin-policy rejection; or
- Cast/receiver failures.

Those conditions retain their existing typed recovery behavior. In
particular, genuine runtime decode incompatibility may request one
capability-aware replan, but it cannot change the attempt's security mode.

## Compatibility

Existing Silo-compatible clients that omit these feature tokens continue to
receive legacy signed media URLs. Existing first-party Vondel clients remain
functional while the readiness setting is disabled.

The change is additive at the API boundary:

- one new capability token;
- one new admin setting;
- no removed or repurposed fields;
- no changed status meaning for legacy attempts.

Jellyfin compatibility is unaffected because this negotiation belongs only to
the native playback-v3 API.

Unknown client feature tokens continue to be ignored according to the current
pre-lock v1 contract. A server that lacks the readiness token naturally keeps
new clients in legacy mode.

## Observability

The server records bounded counters for:

- attempts by `legacy`, `header_api_origin`, and
  `header_authorized_origin` mode;
- readiness-disabled negotiation downgrades;
- media authentication refreshes;
- fresh legacy fallbacks by stable reason;
- untrusted-origin and redirect rejections; and
- playback-start failures partitioned by transport mode.

Clients record the same mode and stable recovery reason locally. Telemetry
must use allowlisted enums and opaque or hashed attempt identifiers. It must
not contain credentials, full URLs, query strings, profile IDs, media paths,
or device serial numbers.

## Rollout

1. Ship client serialization of `software_video_decode_v1` from runtime
   evidence while header-auth readiness remains disabled.
2. Ship the server readiness token and admin setting, defaulting to
   `disabled`.
3. Ship Android and Apple authenticated resource loading, origin containment,
   refresh, and bounded legacy fallback while they continue to withhold the
   feature unless readiness is advertised.
4. Enable `single_or_affine` in a controlled single-replica or verified-affinity
   environment.
5. Monitor start failures, authorization refreshes, fallbacks, and origin
   rejections before broader rollout.
6. Roll back instantly by setting the mode to `disabled`. New attempts return
   to legacy signed URLs without a client release; active attempts retain their
   sticky mode until they stop or recover through the bounded fresh-attempt
   path.

The admin UI must explain that `single_or_affine` is an operator assertion,
not automatic topology detection. It must warn that tokenless API-origin
sessions cannot currently reconstruct on a different API replica.

## Verification requirements

### Server

- capability response distinguishes binary support from deployment readiness;
- readiness defaults disabled and validates only the documented values;
- stale opt-in requests downgrade atomically to legacy when readiness is off;
- header-auth and authorized-origin features remain sticky across replans;
- proxy-origin selection requires the negotiated pair;
- capability and decision fixtures include the readiness behavior;
- logs, persisted plans, and telemetry contain no bearer credential;
- legacy and unknown-client fixtures remain unchanged; and
- admin API and UI tests cover the setting and warning.

### Android

- start and replan serialize the same activation-owned feature set;
- software decode is advertised only from a genuine bounded runtime entry;
- API-origin and selected-media-origin requests are authorized;
- HLS manifests, segments, subtitles, and fonts use the same policy;
- cross-origin redirects and HTTPS downgrades are rejected without header
  leakage;
- one refresh and one fresh legacy fallback are enforced per attempt;
- cancellation and stale ownership publish no retry;
- phone, TV, detail, and Watch Together share one activation bundle; and
- Cast does not inherit sender capabilities or credentials.

### Apple

- start and replan serialize the same activation-owned feature set;
- software decode is advertised only from genuine runtime evidence;
- the owned resource loader authorizes derived AVFoundation requests;
- redirects and origin changes are contained before credentials are applied;
- token renewal rebuilds media loading at the current source position;
- one fresh legacy fallback is enforced per attempt;
- route/display generation changes affect only new attempts; and
- receiver launch remains identity-only.

### Shared protocol fixtures

Android and Apple must consume shared fixtures for:

- readiness absent, disabled, and enabled;
- software-decode-only negotiation;
- API-origin header-authenticated plans;
- plans with one authorized proxy origin;
- sticky replans that try to add or remove protected features;
- authentication refresh and missing-session legacy fallback; and
- rejected cross-origin redirects.

The fixtures prove semantic parity; platform-specific tests remain responsible
for their actual network and media-loading stacks.

## Delivery boundaries

Implementation should be split into coordinated, independently reviewable
changes:

1. server readiness contract, setting, fixtures, admin UI, and telemetry;
2. Android feature projection and authenticated resource transport;
3. Apple feature projection and authenticated resource transport;
4. shared recovery behavior and Watch Together verification;
5. controlled enablement and operational evidence.

No change should enable the server setting by default. The rollout is complete
only when all three repositories implement the same negotiation and recovery
contract and the controlled deployment evidence is recorded.
