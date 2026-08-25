# Header-Authenticated Media Rollout Handoff

Date: 2026-08-24

## Outcome

The server, shared contracts, Android clients, and Apple clients now implement
the same additive playback negotiation contract. Header-authenticated media is
still disabled by default. Legacy signed playback remains the compatibility
path for Silo clients and for Bloem clients that do not negotiate the new
features.

The implementation is fail-closed:

- the server advertises readiness only when the operator selects
  `single_or_affine`;
- clients freeze one feature set for start and replan;
- media credentials are attached only to exact authorized origins;
- redirects must remain on the initiating origin;
- authentication renewal is bounded to one attempt;
- a fresh legacy attempt is bounded to one attempt; and
- receiver playback never inherits sender capabilities or credentials.

## Repository commits

### `bloem-client-contracts`

- `25ee952` — `feat(playback): define authenticated media negotiation`

### `bloem-server`

- `d2589e56` — `docs(playback): design authenticated media rollout`
- `107a7ef0` — `docs(playback): clarify negotiated feature authority`
- `0a0d1609` — `feat(playback): gate authenticated media by deployment readiness`
- `eaeae929` — `feat(playback): expose authenticated media rollout controls`

### `bloem-android`

- `c5f199f` — `feat(playback): negotiate runtime Android playback features`
- `52436d3` — `feat(playback): secure Android authenticated media loading`

### `bloem-apple`

- `0fa7ddc` — `feat(playback): negotiate runtime Apple playback features`
- `dcbfc08` — `feat(playback): secure Apple authenticated media loading`

## Verification evidence

- Contracts: `go test ./...` passed.
- Server focused gate: playback, playback contract/plan store, API handlers,
  and configuration packages passed.
- Android focused gate: identity and playback negotiation, origin containment,
  bounded recovery, and session-controller tests passed; both mobile and TV
  debug Kotlin applications compiled.
- Apple focused gate: 117 tests passed with zero failures. Three AVPlayer
  fixture tests were skipped because the optional disposable fixture origin
  was not supplied; all local authenticated-loader, plan, validator, subtitle,
  and Watch Experience tests ran.
- Diff checks passed in all implementation worktrees before commit.

The generated Xcode app graph still exposes two pre-existing build-fixture
issues outside this rollout: a tvOS-only `onExitCommand` modifier is included
in the generic iOS build, and the tvOS UI-test bootstrap omits the now-required
test Aether engine argument. Swift package build and the affected production
package tests are green; these fixture issues do not change the negotiated
media implementation.

## Compatibility

- No Silo server protocol behavior is removed or repurposed.
- Unknown client tokens remain ignored under the current additive contract.
- Clients that omit the new tokens continue on legacy signed playback.
- A server without the readiness capability naturally keeps new Bloem
  clients in legacy mode.
- Jellyfin compatibility is unaffected because this negotiation applies only
  to the native playback-v3 API.

## Deployment and rollback

1. Deploy server and clients while `playback.authenticated_media_mode` remains
   `disabled`.
2. Confirm the target deployment is a single API replica or has verified
   request affinity for tokenless playback state.
3. Change the setting to `single_or_affine` for a controlled cohort.
4. Observe playback mode, refresh, fallback, untrusted-origin, redirect, and
   start-failure counters. Telemetry must never include credentials or full
   media URLs.
5. Roll back immediately by restoring `disabled`. New attempts return to
   legacy signed playback without a client release; active attempts keep their
   sticky negotiated mode until completion or bounded recovery.

## Remaining operational work

- Run the three optional AVPlayer fixture tests against the disposable
  authenticated media fixture before production enablement.
- Repair the two generated Xcode fixture build issues noted above.
- Record controlled single-replica/affinity deployment evidence before
  changing the default-disabled setting anywhere.
- Do not enable the feature on multi-replica deployments without proven
  affinity or shared tokenless playback state.
