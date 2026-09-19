# Bloem contract adjudications

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
with all 30 transport decisions and their reasons in
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
| Five `scopes.*` discovery cases / v2 | Exactly five named scopes in the reviewed order | Native discovery includes session summaries, library discovery and bulk entitlements. V1 keeps its two-scope projection. Discovery never grants authority. |
| `me.api_key` / v1 | An authenticated unscoped key reads its own exact, nonsecret account projection | Authentication is resolved once by middleware rather than reparsing a key as a JWT. This does not permit session minting, logout or scope escalation. |
| Four `account_capability.*` cases / v2 | Revision is the opaque authority-bound digest, not constant `"1"` | Primary, secondary and profileless capability predicates stay unchanged. |
| Three `state.*` and four `progress.*` cases / v2 | Exact `Cache-Control: no-store, no-transform`, including follow-up reads | Preserve both privacy directives rather than weakening the response. |
| `profiles_create.limit`, `profiles_create.unknown_library_ids` / v1 | Existing status/error codes with safe domain-level error text | Transactional profile mutations do not expose storage implementation details. |
| `profiles_delete.secondary_forbidden` / both | Hidden profile returns `404`, not an existence-revealing `403` | Concealment precedes management authority; deletion remains forbidden. |
| `avatar_upload.typed_nil_panic`, `avatar_upload.meaning` / both | Missing object storage returns `503` with a safe envelope | An unavailable dependency is not a recovered handler panic. |

Only expected responses and the reviewed onboarding follow-up headers change.
Requests, identities, fixtures, mutation sequencing, transport pairing, and
follow-up execution do not. A focused real-router packet executes all 52 transport
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

These are targeted contract checks, not a full-suite, remote-CI, real-provider,
or deployment certification. The [completion handoff](bloem-web-completion-handoff.md)
records the validation scope and remaining operational acceptance.
