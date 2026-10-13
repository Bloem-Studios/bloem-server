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

This is authenticated browse-only policy enforcement, not the anonymous showcase
or a dedicated preview principal. Preview write allowlists, response hygiene,
artwork-class restrictions and isolation from arbitrary public plugin content
belong to that separate design.

Regression coverage lives in the Bloem browse-only handler, middleware, v2 and
plugin tests. Router tests exercise reused credentials across policy revocation
and an existing native HLS ticket after playback is revoked.
