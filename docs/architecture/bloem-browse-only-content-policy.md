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
Dedicated preview principals add the route restrictions below. Guest sessions,
response hygiene, artwork-class restrictions and the prospect UI remain separate
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
Personal session housekeeping and device approval are listed separately for
later guest-session restrictions.

Both plugin authentication paths treat preview principals as anonymous, whether
the credential is a bearer token, query token, plugin cookie or API key. Existing
public discovery and sign-in artwork keep their public behavior.
