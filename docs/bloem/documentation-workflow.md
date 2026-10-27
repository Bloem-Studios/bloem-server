# Maintaining Bloem documentation

Documentation follows the implemented source. Keep API contracts, user instructions,
architecture and deployment evidence distinct, and link between them. A generated
DTO is not proof that a native client supports a workflow; a passing health check
is not proof of provider or playback acceptance.

This page is for anyone changing Bloem's documentation. Bloem Server is a tracking
fork of Silo Server: Silo's own Markdown files stay exactly as Silo wrote them, and
Bloem's material lives in Bloem-owned files (see [fork provenance](../../FORK.md)
and the [documentation index](README.md)).

## What Silo does

The reviewed Silo server baseline is `3e2d306b2` (October 27, 2026), the Silo
revision Bloem `main` integrates. Silo's [`docs/README.md`](../README.md) was last
changed in `42b39765b` (Silo #1635, September 28, 2026), which moved the operator
guides out of the server repository; it is unchanged at `3e2d306b2`. It sends
people running a server to Silo's user manual and keeps this folder for people
changing the server. Architecture and invariants live under `docs/architecture/`;
API references live in `docs/*-api.md`. Go registrations generate OpenAPI and the
server embeds Swagger UI at `/api/v2/docs` ([api-docs.md](../api-docs.md)). New
plans are temporary working files. Between the previous baseline `6de6d5e5e` and
`3e2d306b2`, Silo's only new document is
[`docs/architecture/non-utf8-paths.md`](../architecture/non-utf8-paths.md), an
architecture page within that model.

Silo's public manual is a separate repository,
[`Silo-Server/siloserver.org`](https://github.com/Silo-Server/siloserver.org),
reviewed at `b2197cad7c7384512fa6f76f82934682fa54d6f5`; its layout below was
re-checked at `35d859752` (October 24, 2026). It uses Astro/Starlight,
plain Markdown in `src/content/docs/docs/`, a curated task-based sidebar in
`src/data/sidebar.mjs`, redirects in `src/data/docs-redirects.mjs`, edit links,
last-updated metadata and `starlight-links-validator` during the build.
Its navigation groups getting started, viewer tasks, running a server and
reference material. The site source is AGPL-3.0-or-later; Silo's trademarks and
visual identity are separately restricted.

Bloem adopts that separation and task-based navigation. Its portable operator and
viewer pages currently remain in this repository, indexed by the
[manual index](overlays/wiki/index.md); no separately hosted Bloem manual is
established by this refresh. A future Starlight site should publish those maintained
pages or move them with redirects, rather than create independently edited copies.
Do not copy Silo's branding, domain, deployment credentials or removed protocol
instructions into a Bloem site. Keep license/provenance notices on reused source.

## Where a change belongs

| Audience or change | Maintained source |
| --- | --- |
| Product overview, provenance and current source status | [Bloem front page](../../.github/README.md), [integration changelog](CHANGELOG.md) |
| Fork and merge policy | [FORK.md](../../FORK.md), [agent rules](../../BLOEM_AGENTS.md) |
| CI, the daily upstream sync and the published image | [Bloem CI](ci.md) |
| Viewer tasks | [User guide](../wiki/user-guide.md) |
| Installation, accounts, libraries, maintenance | [Admin guide](../wiki/admin-guide.md), [Docker guide](overlays/wiki/deployment/docker.md) |
| Upstream developer/API behavior | Existing Silo reference plus the relevant [Bloem overlay](README.md#overlays-on-upstream-documents) |
| Native API wire behavior | [Native reference](../bloem-api-reference.md), owned `internal/apiv2/bloem_*document*.go` declarations |
| Security, tenancy, storage and playback invariants | Bloem-owned files under `docs/architecture/`, listed in the [index](README.md) |
| Generated contract coverage | [API catalogue](api-catalogue.md) |
| Exact deployment evidence | Dated `docs/operations/` records with revision, checks and limits |
| Temporary implementation plan | Ignored `docs/superpowers/`; distill durable facts before committing |

Keep Silo-owned Markdown unchanged: a Markdown file that also exists in Silo's
`main` is Silo's, and Bloem differences go in an **overlay**, a Bloem-owned file
under `overlays/`, usually at the upstream document's path relative to `docs/` (or
the repository root), that names the upstream section and the
Bloem replacement or addition. Silo-owned files Bloem does edit are **seams**, each
declared in `contracts/seams.txt` (306 entries at `e852f92bf`) and checked by
`make verify-seams`; the ledger must not grow for a documentation refresh. Historical deployment records retain
their original facts; a current landing page must not present an old record as
live health.

Operator/viewer pages use portable Markdown and frontmatter with `title`,
`description`, `summary`, `tags`, `audience`, `last_reviewed` and `related`.
Review dates describe a source review, not a device acceptance run. Link to
architecture for technical invariants instead of duplicating them in user steps.

## API and Swagger sources

| Surface | Source and generated output | Viewer and authority |
| --- | --- | --- |
| `/api/v2` | `internal/apiv2` registries → `contracts/api/v2/openapi.json` | `/api/v2/docs` loads `/api/v2/openapi.json`; normal account/profile and acting-admin rules apply |
| `/api/bloem/v1` | Owned Bloem registrations → `contracts/api/bloem/v1/openapi.json` | Load the file in trusted local Swagger tooling; the v2 viewer does not include these extensions |
| Frozen alpha `/api/v1` | Mounted handlers, upstream API references, [contract overlay](overlays/architecture/api-contract.md) and inventory | Bridge and retirement policy is separate from native extensions |
| Native client DTOs | `contracts/client/v1/registry.json` and Go wire types → Kotlin/Swift trees and graph digest | Server-owned generated files require explicit adoption by each client |
| Settings | `contracts/settings/v1/` → Go/TypeScript/Kotlin/Swift bindings | Inheritance, scope and null semantics are part of the contract |

Both OpenAPI artifacts are generated without a database or production credentials.
For a native chi handler, document the real token type, path spelling, trailing
slash, request wrapper, success status, error envelope and revision convention.
Do not reuse v2 Problem/If-Match rules unless the handler actually implements them.
Document optional service dependencies and capability gates. Binary and streaming
routes need their actual media type and authorization boundary, not a JSON stub.

The [catalogue](api-catalogue.md) reports any inventoried method/path without a
typed native declaration. Administration and Live TV declarations now cover the
previous inventory gaps; production mount tests enforce method-level agreement.
An all-method router mount is not an operation for every HTTP verb.
`methodOnly` wrappers are resolved by the catalogue generator; unknown dispatchers
remain unresolved. Production mount tests supplement the inventory for conditional
groups. Never add invented schemas simply to make a coverage number reach zero.

Document-only Go schema mirrors are separate from native-client DTO roots.
Give each exported mirror an explicit reason in the server's
[client coverage allowlist](../../contracts/client/v1/coverage.json), and verify
its wire shape against the handler. Adding a schema does not establish adoption
by either native client.

## Regenerate and verify

Commands assume the repository root and the Go toolchain declared in `go.mod`.
Keep `SYNC_CLIENTS` unset or `0`; copying to sibling clients is a separate action.

```sh
GOWORK=off make apiv2-openapi bloem-openapi route-inventory
GOWORK=off make apiv2-web-types settings-bindings
python3 scripts/bloem-api-catalogue.py
GOWORK=off make verify-apiv2-openapi verify-bloem-openapi verify-apiv2-web-types
GOWORK=off make verify-settings-bindings-all verify-client-digest verify-client-dtos verify-client-coverage
GOWORK=off make verify-route-inventory verify-migration-ledger verify-apiv2-contract
make verify-bloem-api-catalogue
make verify-local-paths verify-seams
```

When Go wire types or registered roots change, also run `make client-dtos`.
Run `make client-digest` only after reviewing and recording a genuine graph change.
For a documentation-only update, `verify-client-dtos` regenerates into a temporary
directory using the existing source stamp, avoiding meaningless revision churn.
Inspect diffs; never change a digest or breaking approval solely to silence a gate.

For changed v2 responses, regenerate and check `apiv2-fixtures`; for a playback
protocol change use `playback-fixtures` and `verify-playback-fixtures`. Check
`verify-settings-bindings-all` prerequisites in the Makefile when a client toolchain
is unavailable; do not present an omitted platform runner as a pass.

Focused native mounting/schema checks:

```sh
go test -count=1 ./internal/api -run 'Test(EveryDocumentedNative|UndocumentedNative|NativeStorageDocumentMatchesMounted|BookRequestDocumentMatchesMounted)'
```

Also run focused `internal/apiv2` document tests for the feature changed. Check local
links and heading anchors in edited pages, and review navigation labels against
the source UI. If publishing a future manual site, build it with link validation
enabled. Full Go/web suites and backend switching are integration/release evidence,
not a substitute for checking documentation accuracy.

## October integration checklist

Facts a page about current behavior must not get wrong. Each was re-checked
against the code at `e852f92bf` (Silo through `3e2d306b2`).

- Account policy lives on `users`, as in Silo (`c60b7008e`, migration
  `20261026003000_bloem_account_policy_on_users`). An organization membership
  only links an account to its one organization (`UNIQUE (account_id)`); do not
  describe memberships as the policy authority or describe mirrored or
  finalized membership-policy phases as current.
- Nullable account `max_profiles` inherits the group limit; it does not mean
  unlimited profiles (`internal/accesspolicy/policy.go`). Explicit account
  overrides win, and a group with a managed profile limit applies its ceiling as
  well. Both client releases need the
  [contract transition](../architecture/bloem-contract-adjudications.md).
- Silo removed its administrator color, font and custom-CSS controls (Silo
  #2413) and kept one theme (Silo #1534). Bloem organization, campaign and
  seasonal branding remain separate features.
- Libraries can combine multiple local paths and selected roots from several
  storage sources ([native storage](../architecture/bloem-native-storage.md)).
  Preserve folder navigation and existing selections; provider and playback
  readiness remain separate from configuring a source.
- Silo's Jellyfin and Audiobookshelf compatibility servers run on their own ports
  (removed by Bloem on October 13, restored on October 18 in `2393c41ee`);
  Bloem's companion applications and compatibility gateway stay removed. See
  [protocol compatibility](../architecture/bloem-compatibility-removal.md).
- Pastime managed tracking is removed (merge `4835a698e`, migration
  `20261013231500_remove_pastime_managed_tracking`). Do not restore its
  API/setup guidance; any future ordinary plugin integration has its own
  contract. See [Pastime removal](../architecture/bloem-pastime-removal.md).
- Music is removed (`f9e1447f3`, migration `20261026020000_bloem_remove_music`).
  Silo has no music library type either: library types are movie, series,
  mixed, audiobook, ebook, podcast and manga (`internal/librarykind`).
- Lifecycle idempotency receipts are removed (`b017bfe84`); the
  `lifecycle_idempotency_v1` tokens are no longer advertised. Describe retries
  with the [retrying mutations](../bloem-api-reference.md#retrying-mutations)
  rules, not an `Idempotency-Key` replay for every lifecycle write.
- Promotions and ambience evaluate in-process (`310c545f9`); there are no
  bundled presentation worker binaries or `BLOEM_PRESENTATION_PLUGIN_DIR`.
- The header-authenticated media deployment gate (`545ed3a33`, October 9) and the
  `playback.strict_reconstruct_admission` setting (`e452f8fef`, October 26) are
  gone. Do not document them as operator controls.
- AI providers are connected once under AI Services and chosen per job (Silo
  #3106, merged in `68070f04b`); the embedding provider is chosen there too
  (Silo #3107). Pages that describe one key per AI feature are stale.
- Authenticated browse-only content policy does not by itself establish a public
  showcase. The server side of the preview principal, signed guest sessions and
  catalogue hygiene is in this repository
  ([browse-only policy](../architecture/bloem-browse-only-content-policy.md));
  the Garden launch flow lives in Garden's own repository and is not checked
  here. Verify both deployed revisions, preview-host configuration and the
  launch/exchange journey before describing a live prospect flow as enabled. The
  [October 15 receipt](../operations/2026-10-15-preview-server-deployment.md)
  recorded public preview as disabled; no later receipt records enabling it.
  Native preview controls and later showcase presentation remain separate
  follow-up work.

When finishing, state the source revision, changed docs/artifacts, verification
scope and remaining gaps. A source refresh alone neither deploys a server nor
publishes a manual or adopts contracts into a client release.
