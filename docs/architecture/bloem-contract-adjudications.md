# Bloem contract adjudications

## October 14 upstream contract transition

The Silo sync through `ea3c10a7f` adopts two upstream pre-lock decisions:
[Silo #1801](https://github.com/Silo-Server/silo-server/pull/1801) makes the
stored account profile limit nullable to express group inheritance, and
[Silo #2413](https://github.com/Silo-Server/silo-server/pull/2413) removes
administrator CSS, font and color overrides. The four exact breaking-change
fingerprints against the prior Bloem baseline are recorded in
`contracts/api/v2/breaking-approvals.json`, with those upstream PR numbers as
their approval provenance. No wildcard or post-lock exemption is introduced.

The web consumers and server-maintained Kotlin/Swift contracts follow the
upstream shapes. Bloem's organization and seasonal branding remain separate.
Before rollout, both native client releases need their generated-contract
adoption checked, especially nullable `max_profiles`. This local server merge
does not update or certify sibling client releases.

The auth scenario catalog adds Silo's `jellyfin_quick_connect` capability field;
Bloem answers `false` because that protocol is retired. Its baseline digest was
refreshed after reviewing those three added field-presence assertions. The
existing adjudicated scenarios and their transport decisions are unchanged.

## Decision and scope

The 2026-09-19 CI follow-through reconciles historical Silo fixtures with behavior
already shipped by this tracking fork. It does **not** change production handlers,
authorization, response serialization, or routing to satisfy a test. It does not
restore credential disclosure, typed-nil panics, or hidden-profile enumeration.

The original scenario JSON, route snapshots, and upstream acceptance selectors
remain unchanged. The ordinary Bloem scenario gate applies an explicit,
source-pinned downstream expectation layer. This is a deliberate contract decision,
not a claim that the original Silo expectations passed unchanged.

The implementation is
[`bloem_adjudications.go`](../../internal/scenariocatalog/bloem_adjudications.go),
with all 53 transport decisions and their reasons in
[`bloem_adjudications.json`](../../internal/scenariocatalog/bloem_adjudications.json).
The loader checks SHA-256 pins for all six affected baseline catalogs and compares
each supplied scenario and row with its original. A changed request, principal,
row, or original assertion fails closed and requires renewed review. It returns
copies; the frozen loader and its historical selectors are not rewritten.

## Scenario decisions

| Cases / transport | Current Bloem contract | Reason |
|---|---|---|
| `adm_inv_create.invalid_email` / v2 | `422 validation_failed`, with exactly one safe `body.email` field diagnostic | Required by [Problem Details](api-contract.md#problem-details); v1 remains `400 invalid_email`. |
| `resources.ok`, `resources.unsampled` / v2 | Unsampled resources explicitly include `stale: true` | Availability remains false and GPU entries remain empty. Do not discard freshness information. |
| `keys_list.meaning`, `keys_list.shape` / v1 | `key` is absent, including null/empty values; `key_prefix` is present and `revision` is a positive integer | The [recorded pre-lock removal](v1-scope.md#breaking-removals-taken-before-lock) makes secrets creation-only. Ownership, scope and ordering assertions stay. |
| Five `scopes.*` discovery cases / v2 | Exactly seven named scopes in the reviewed order | Native discovery includes session summaries, library discovery, bulk entitlements, storefront template provisioning (`admin:entitlements:accounts`) and preview administration (`admin:preview`). V1 keeps its two-scope projection. Discovery never grants authority. |
| `me.api_key` / v1 | An authenticated unscoped key reads its own exact, nonsecret account projection | Authentication is resolved once by middleware rather than reparsing a key as a JWT. This does not permit session minting, logout or scope escalation. |
| `me.api_key`, `me.ok`, `me.disabled_account` / v2 | Silo's account keys plus `preview` and `playback_allowed` | Bloem's account projection reports the guest preview binding (null otherwise) and playback permission. Everything else Silo pins is unchanged. |
| `setup_status.db_down` / v1, `signup_status.db_down` and `capability.ok` / v1 and v2 | `503 unavailable`, "Preview host configuration is unavailable." | With the database unreachable the preview host guard cannot read its settings, so it refuses every auth route before routing; a GET there can start a sign-in. Anonymous routes elsewhere, including liveness and readiness, keep Silo's answers. |
| Four `account_capability.*` cases / v2 | Revision is the opaque authority-bound digest, not constant `"1"` | Primary, secondary and profileless capability predicates stay unchanged. |
| Three `state.*` and four `progress.*` cases / v2 | Exact `Cache-Control: no-store, no-transform`, including follow-up reads | Preserve both privacy directives rather than weakening the response. |
| `profiles_create.limit`, `profiles_create.unknown_library_ids` / v1 | Existing status/error codes with safe domain-level error text | Transactional profile mutations do not expose storage implementation details. |
| `profiles_delete.secondary_forbidden` / both | Hidden profile returns `404`, not an existence-revealing `403` | Concealment precedes management authority; deletion remains forbidden. |
| `avatar_upload.typed_nil_panic`, `avatar_upload.meaning` / both | Missing object storage returns `503` with a safe envelope | An unavailable dependency is not a recovered handler panic. |
| Four `sessions.*` cases / v2 | The listing also carries `current_session`; each session also carries `current` and `last_seen_at` | Silo's v2 session listing added them after the oracle was frozen. Silo wins. |
| `profiles_list.ok` / v2 | The listing also carries `max_advisory_age_supported` and `require_advisory_age_supported` | Silo's v2 profile listing added them after the oracle was frozen. Silo wins. |
| Ten `device_lookup.*` cases / v2 | `server_name` is `Bloem` | Bloem's default display name (`branding.DefaultServerName`); the oracle expects Silo's brand. |

Only expected responses and the reviewed onboarding follow-up headers change.
Requests, identities, fixtures, mutation sequencing, transport pairing, and
follow-up execution do not. A focused real-router packet executes all 82 transport
leaves, including the unchanged partners. Negative tests reject disclosed keys
and changes to the pinned originals.

The older named Silo acceptance targets still certify their original upstream
contracts. They are not relabeled as Bloem compatibility evidence; in particular,
a target requiring reusable secrets on list reads is not a valid Bloem release
oracle. Use the normal Bloem scenario gate or the focused packet below for these
adjudicated cases. No test is excluded from the ordinary gate.

## Route decisions

[`bloem_v1_adjudication_test.go`](../../internal/api/bloem_v1_adjudication_test.go)
pins the unchanged route snapshots and lists every affected method/path:

- Three OAuth routes are existing upstream Silo routes, verified in the tracked
  upstream router: `POST /api/v1/auth/oauth/complete`,
  `POST /api/v1/auth/oauth/{install_id}/init`, and
  `GET /api/v1/auth/oauth/{install_id}/callback`. They are not new fork-only v1
  features. The full surface check now requires these additions explicitly.
- The 28 historical `/api/v1/livetv/...` entries describe Bloem's Prairie-derived
  feature, not an upstream Silo capability. Their maintained home is
  `/api/bloem/v1/livetv/...`, as already implemented by the native Live TV work.
  This review records the namespace correction; it does not introduce aliases,
  redirects, or another v1 removal at runtime. Older callers of the fork-only
  prefix must migrate; they are not claimed compatible.

There is no prefix-wide ignore rule. Every listed old route must be absent and
its exact native method/path must be mounted, including `HEAD` and all
administrative writes. A missing native replacement, restored legacy alias,
missing OAuth addition, changed snapshot, or unrelated v1 addition/removal fails.
The existing mounted authority and delivery tests remain separate checks of
profile, PIN, administrator and stream-proof enforcement.

## Focused verification

Commands assume the repository root. Use separate disposable, migrated
application and scenario databases; scenario reseeding is destructive.

```sh
go test -count=1 ./internal/scenariocatalog -run '^TestBloemAdjudications'
go test -count=1 ./internal/scenariocatalog/executor \
  -run '^TestBloem(AdjudicatedScenarios|AdjudicationRejectsAPIKeyDisclosure)$'
go test -count=1 ./internal/api \
  -run '^(TestV1RouteSurface.*|TestV1SiloRouteContractIsNeverNarrowed|TestBloemV1AdjudicationClosedSet)$'
```

The focused real-router packet passed **52 transport leaves / 99 hierarchical
events** without skips, and the route packet passed **9 hierarchical events**.
These are targeted results, not a full-suite or real-provider certification.
Remote CI for `418a18b7d` subsequently passed the API route tests but timed out in
the ordinary scenario executor after 20 minutes. No assertion-failure markers
were reported; unfinished scenarios remain unverified.

The [deployment record](../operations/2026-09-19-xtream-deployment.md) records the
completed rollout and narrowly approved timeout exception; it does not mark CI
green. The [completion handoff](bloem-web-completion-handoff.md) records remaining
operational acceptance.
