# Browse-only content policy

Bloem's existing group policy can grant catalogue visibility while denying
playback. Playback denial must prevent content delivery as well as video session
creation. It does not remove descriptions, seasons, episodes, file descriptors
or theme-song descriptions from an otherwise accessible catalogue.

The native listener enforces these boundaries:

- The composed native ebook reader checks current playback permission before
  resolving a file. This covers v1 and v2 GET/HEAD, local ebook/comic/manga files,
  native storage, ranges, conditional requests and conversion. Reading is a
  playback permission; download permission is independent.
- The v2 operation gate refuses trickplay manifests and theme playback
  authorization before handlers can sign URLs or dispatch work.
- The composed theme service refuses playback-denied authorization, and wraps
  the grant consumer's fresh viewer resolution with a playback check. Existing
  grants retain the host's login, account, revision, profile and file checks.
  Revoked grants receive the existing invalid-grant response without file bytes.
- Live TV session starts require playback permission. Delivery requires both
  Live TV and playback permission from the freshly resolved scope; an unexpired
  stream token cannot bypass revocation. Guide/channel browsing keeps the
  separate Live TV permission.
- Both plugin mounts resolve current tenant and viewer policy after their
  independent authentication. Playback denial is explicit, rather than treating
  the viewer as anonymous, and precedes plugin dispatch or asset file opening.
  Bearer, query, plugin-launch-cookie and unscoped API-key credentials use the
  same check. Direct-profile sessions retain their existing plugin prohibition.
  Anonymous public descriptors and public sign-in icons keep their existing
  behavior.

Transport restrictions are carried by the Bloem content adapters. They do not
become catalogue predicates. Independently authenticated content endpoints
reconstruct tenant authority from their validated account/profile identity before
resolving policy; normal viewer endpoints keep the host's authentication, profile
and PIN gates.

The implementation uses Bloem-owned adapters and existing router/plugin seams.
Do not wire the underlying shared ebook or theme service directly into the Bloem
listener: doing so omits the added policy boundary.

These transport checks also protect ordinary accounts with playback disabled.
Dedicated preview principals and guest sessions add the restrictions below.
Response hygiene, artwork-class restrictions and the prospect UI remain separate
parts of the preview design.

Regression coverage lives in the Bloem browse-only handler, middleware, v2 and
plugin tests. Router tests exercise reused credentials across policy revocation
and an existing native HLS ticket after playback is revoked.

## Preview principals

The protected `bloem-preview` entitlement template permits catalogue browsing
while denying playback, downloads, transcoding, requests and permissions. Only
its library selection may be revised. Database constraints pin those limits on
template revisions and materialized access groups; the template cannot be
archived or used as the parent of a derived cohort.

A non-admin account with a membership or profile group from that template is a
preview principal. Classification is read with current session authority on each
request. Per-account and profile overrides cannot restore the denied permissions.
Conversion therefore affects existing credentials on their next request,
including v1 websocket tickets minted before it. API keys owned by preview
principals are refused.

Two short-lived credentials are not classified: a direct-download link (`dl`) and
an Apple push display token. Neither reaches the preview gate as a preview
principal, so they rely on the policy checks behind them. A preview principal
cannot mint either. One minted before a conversion still meets the downloads
policy (downloads are off) or can only read notification text.

Authenticated v1 and native routes use the exact method/route allowlist in
`internal/api/bloem_preview_routes.go`. Delegated v2 routes use the operation
allowlist in `internal/apiv2/bloem_preview_gate.go`. Unlisted operations are denied
by default, including new upstream operations. Preview refusals carry
`X-Bloem-Denial: preview_only`; v2 retains the `permission_denied` problem type.
Boot and catalogue reads pass their existing profile, tenant and library gates.
Personal previews retain session housekeeping and device approval. Guests
are refused those operations and audience-ticket minting and consumption.

Both plugin authentication paths treat preview principals as anonymous, whether
the credential is a bearer token, query token, plugin cookie or API key. Existing
public discovery and sign-in artwork keep their public behavior.


## Guest preview sessions

`POST /api/bloem/v1/preview/sessions` exchanges a signed launch ticket only on the
configured preview host. Ed25519 verification checks issuer, server audience,
host, issue time, strict expiry, ticket identifier and optional item/reference.
The issue-to-expiry window is at most 60 seconds. Successful exchanges retain
consumed identifiers in PostgreSQL until expiry, so replay is refused across
processes and restarts. Expired receipts are swept at most once per minute.

Sessions carry `auth_method=preview_guest` and belong to the configured shared
account. Database guards serialize credential creation with account assignment:
ordinary sessions and API keys cannot race the assignment and its revocation.
The generic session repository cannot create guest sessions. Guest classification
is forced independently of the account's group; guest account responses retain
only boot identity and expose no contact details, permissions or downloads.

Absolute expiry is set at exchange (two hours by default). Idle expiry defaults
to 20 minutes. Access tokens are capped at 15 minutes and the remaining session
lifetime. Refresh never extends either deadline. A valid refresh token can reuse
its session during a fresh ticket exchange, within the same database transaction.
Disabling preview revokes every guest session in the settings transaction.
Security settings are read fresh; presentation data may be cached.

Host enforcement uses the request's original `Host`, never forwarded-host
metadata. Guest credentials are refused on every other host before public or
optional-auth handlers run. The preview host refuses other sign-ins and plugin
surfaces, and accepts only guest credentials. A settings-read outage fails closed.

Capacity is counted and allocated under the same PostgreSQL advisory lock as
settings changes, with a default of 200 live guests. An IPv4 address or IPv6 /64
has at most three live sessions; the oldest is revoked for a fourth. An opaque
HMAC bucket derived from the shared server signing secret keeps that cap coherent
across processes. No address is stored in the session tables. Launch-attempt
limits are rolling-hour counters in each API process: 20 per client bucket, with
bounded memory. Guest HTTP traffic, including public endpoints, is capped at
60 requests/second (burst 120); authenticated sessions also have their own
10 requests/second cap (burst 30), before the normal server-wide budget.

The resolved address is retained only in request memory for admission. Guest
requests remove addresses and browser/client headers from observer context and
skip request/activity writers. Expired signed guest credentials receive the same
privacy treatment. Activity filtering reads only a cached account snapshot and
performs no database I/O on the writer's request path. Aggregate preview event
counters and setup/admin controls belong to the later preview setup milestone.
