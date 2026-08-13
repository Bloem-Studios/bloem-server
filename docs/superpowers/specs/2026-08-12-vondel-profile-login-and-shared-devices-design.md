# Vondel Profile Login and Shared Devices Design

**Date:** 2026-08-12

**Status:** Approved

**Scope:** Add optional direct credentials to Vondel profiles while preserving Silo-compatible household login and making shared-client profile switching convenient and revocable.

## Decision

Vondel keeps its current household ownership model:

- A `users` account owns administration, invitations, service limits, permissions, and one or more `user_profiles`.
- The primary profile and household account share the account's existing email/password identity.
- Secondary profiles remain local-only by default and require neither email nor password.
- A secondary profile gains a direct login only when its owner explicitly enables one with a globally unique email and password.
- Silo-compatible clients continue to authenticate the household and then select a profile.
- New Vondel clients can authenticate directly into one profile or pair as a shared household device.

Shared clients do not require every household member's password. The owner approves the device once, selects the profiles it may access, and Vondel issues a revocable device-bound grant. Switching among granted profiles is immediate, subject to an optional profile PIN.

## Goals

- Preserve the existing Silo account-login and profile-picker flow without response-shape regressions.
- Allow a secondary profile to become a private, independently authenticated identity without becoming a separate household account.
- Make personal-device login go directly to the intended profile.
- Make TV and other shared-device setup require only one owner approval rather than every profile password.
- Keep profile permissions, progress, preferences, library restrictions, content ratings, and adult-content access authoritative in the existing profile model.
- Make every device grant and session independently revocable.

## Non-Goals

- Turn profiles into independent billing or administrative accounts.
- Require credentials for existing or newly created secondary profiles.
- Let a secondary profile enumerate sibling profiles or administer the household.
- Use a short PIN for remote authentication.
- Store household or profile passwords on clients.
- Make direct-profile login mandatory for Silo-compatible clients.

## Identity Model

### Household and Primary Profile

The household account's globally unique email/password remains the primary identity. It has two intentionally different authentication outcomes:

- Legacy/Silo endpoint: creates a household session and exposes the compatible profile-picker flow.
- New Vondel direct endpoint: creates a profile-bound session for the primary profile without showing a picker.

The primary profile does not need a second email address or password.

### Secondary Profiles

A secondary profile has two states:

- Local-only: name, avatar, PIN, preferences, permissions, and media state, but no remote credential.
- Direct-login enabled: a globally unique email plus password credential maps to the existing parent user ID and exactly one profile ID.

Direct-login enrollment is explicit. Disabling it removes remote credential use and revokes its direct sessions without deleting the profile or its media state.

### Global Login Namespace

All household-account and enabled profile emails share one case-insensitive global namespace. An email cannot identify both an account and a secondary profile or two different profiles. Creation and update enforce uniqueness transactionally so concurrent enrollment cannot create ambiguity.

Authentication failures do not reveal whether an email belongs to an account, profile, disabled identity, or no identity.

## Session Modes

Every access and refresh session carries an explicit mode:

- `household`: authenticated through the legacy account flow; may use the profile picker but has no media-profile context until selection.
- `profile`: fixed to one `(user_id, profile_id)`; cannot enumerate or switch to siblings.
- `shared_device`: bound to one device grant; may list and select only profiles currently allowed by that grant.
- `admin`: an explicitly elevated household-owner operation, never inferred merely because the current media profile is primary.

Profile-bound authorization always uses both parent user ID and fixed profile ID. No handler may accept a caller-supplied profile ID that differs from the session binding.

## Shared Device Pairing

TVs and other shared clients default to device pairing:

1. The client requests a short-lived, single-use pairing code and displays it.
2. The primary owner opens an authenticated Vondel web/mobile session and enters or approves the code.
3. The owner names the device and selects which profiles it may access.
4. Vondel stores a device grant and returns a revocable device credential through the pending pairing channel.
5. The device lists only granted profiles and can switch between them without profile passwords.

Pairing codes are high-entropy despite their short display form, rate-limited, expire quickly, bind to the requesting device key, and cannot be replayed after approval or denial. The stored grant, not a self-contained token list, is authoritative for allowed profiles. Removing a profile from the grant takes effect immediately.

Clients store device and refresh credentials only in platform secure storage.

## Profile Switching and Local Locks

- A household session may display all profiles the account is allowed to select.
- A shared-device session may display only profiles in its current server-side grant.
- A direct-profile session never displays or switches to siblings.
- Selecting a profile without a PIN is immediate.
- Selecting a PIN-protected profile requires its rate-limited PIN unless the device holds a current remembered unlock.
- A PIN is a local/shared-device privacy control, not a remote login credential.
- Leaving a protected profile relocks it unless the user explicitly enabled “remember on this device.”
- Remembered unlocks are server-side, scoped to device grant plus profile, bounded by expiry, and revocable.

The owner may reset a secondary profile's password or PIN but cannot retrieve it. A reset invalidates that profile's direct sessions and remembered unlocks. The owner does not silently bypass a protected profile; access requires reset or an explicit policy-defined owner recovery action that is fully audited.

## Authorization and Adult Content

The existing profile permission system remains authoritative. Session mode changes how a profile is selected, not what it may access.

Adult profiles and libraries require explicit profile authorization and may be PIN locked. They are absent from a shared device unless the grant explicitly includes them. Unauthorized sessions receive no adult:

- profile-picker entries;
- catalog/search results or counts;
- recommendations, history, or activity;
- artwork or image cache references;
- event payloads or notification hints;
- timing-distinguishable existence responses.

Every operation is reauthorized against current account, profile, library, and device-grant state. Token claims alone do not preserve access after revocation.

## Credentials and Recovery

- Passwords use Vondel's existing password hashing policy and strength rules.
- Profile password hashes are stored separately from profile PIN hashes.
- Raw passwords, PINs, pairing codes, access tokens, and reset tokens never appear in logs or events.
- The primary owner can create, change, disable, or reset a secondary profile's direct-login credential.
- A directly authenticated profile can change only its own email/password after reauthentication.
- Password reset and email change revoke the affected profile's refresh sessions.
- Household password reset revokes household, primary-profile, and admin sessions according to the existing account policy; it does not expose or silently replace secondary credentials.
- Recovery responses are constant-shape and rate-limited to resist account enumeration.

## Client Capability and Flows

Auth capability discovery advertises:

- `legacy_household_login`;
- `direct_profile_login`;
- `shared_device_pairing`;
- profile PIN/unlock support and remembered-unlock policy.

Existing Silo clients continue using the unchanged legacy endpoints and profile picker. New clients select a flow based on capability discovery:

- Personal phones, tablets, and computers default to direct-profile login.
- Apple TV, Android TV, and other shared screens default to pairing.
- The primary credentials can be used in direct mode on a personal device or legacy mode where a household picker is desired.
- Servers without the new capabilities automatically use the legacy Silo flow.

Clients never infer support from server version strings.

## API Boundaries

The implementation adds versioned endpoints for:

- auth capability discovery;
- direct-profile login and refresh;
- secondary direct-login enrollment, update, disable, and reset;
- pairing-code creation, approval/denial, polling, and exchange;
- device-grant list, rename, profile membership update, and revoke;
- shared-device profile list/select;
- PIN unlock and remembered-unlock create/revoke.

Legacy login and profile-selection endpoints retain their existing wire shapes. New responses carry explicit session mode, fixed profile identity where applicable, device identity, capability revision, and refresh semantics.

## Migration

The database migration is additive:

- Existing users remain household identities and primary-profile direct identities.
- Existing secondary profiles remain local-only with no invented email or password.
- Existing profile PINs remain valid for legacy and shared-device switching.
- Existing sessions keep their legacy semantics until normal expiry; no migration silently widens their access.
- New credential and grant tables reference existing `(user_id, profile_id)` identities and cascade safely when profiles or accounts are removed.

Rollout order is server schema and capability discovery, web administration, shared-device pairing, clean-room Apple/Android support, then optional direct-login enrollment. Silo compatibility remains available throughout.

## Failure Behavior

- Expired, denied, replayed, or mismatched pairing codes fail without creating a grant.
- A revoked device grant invalidates new requests immediately.
- Revocation during playback follows Vondel's configured session-revocation policy; it never authorizes a new segment or refreshed URL after revocation.
- If the server is unavailable during pairing, the client remains untrusted and offers a retry; it does not create local profile authority.
- Disabled accounts deny every contained profile. Disabled direct login denies only that credential unless the account/profile itself is disabled.
- A missing or no-longer-granted profile disappears from the device picker and its cached state is removed.
- Credential conflict transactions fail atomically with a non-enumerating user-facing error and an auditable administrative diagnostic.

## Verification

Server and client conformance must cover:

- unchanged legacy household login and profile-picker response shapes;
- primary direct login using household credentials;
- optional secondary enrollment, direct login, disable, reset, and re-enable;
- local-only secondary profiles with no credentials;
- case-insensitive global email uniqueness under concurrent operations;
- household, profile, shared-device, and admin session boundaries;
- pairing approval, denial, expiry, replay rejection, device-key binding, and revocation;
- granted-profile selection, PIN unlock, remembered unlock, relock, and remote removal;
- token refresh, password rotation, reset, account/profile disable, and session invalidation;
- inability of direct secondary sessions to enumerate siblings or perform household administration;
- complete adult-content non-disclosure for unauthorized profiles and device grants;
- web, iOS, iPadOS, macOS, tvOS, Android, and Android TV flows;
- automatic fallback to legacy login against official-compatible Silo servers.

Acceptance runs against the dedicated Vondel development server with personal and shared device fixtures, multiple local-only and direct-login profiles, and separately permissioned adult content.
