# Maintaining Bloem documentation

Documentation follows the implemented source. Keep API contracts, user instructions,
architecture and deployment evidence distinct, and link between them. A generated
DTO is not proof that a native client supports a workflow; a passing health check
is not proof of provider or playback acceptance.

## What Silo does

The reviewed Silo server baseline is `ea3c10a7f`. Its
[`docs/README.md`](../README.md) separates developer references from the public
manual. Architecture and invariants live under `docs/architecture/`; API references
live in `docs/*-api.md`. Go registrations generate OpenAPI and the server embeds
Swagger UI at `/api/v2/docs`. New plans are temporary working files.

Silo's public manual is a separate repository,
[`Silo-Server/siloserver.org`](https://github.com/Silo-Server/siloserver.org),
reviewed at `b2197cad7c7384512fa6f76f82934682fa54d6f5`. It uses Astro/Starlight,
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
| Product overview and current source status | [Bloem front page](../../.github/README.md), [integration changelog](CHANGELOG.md) |
| Viewer tasks | [User guide](../wiki/user-guide.md) |
| Installation, accounts, libraries, maintenance | [Admin guide](../wiki/admin-guide.md), [Docker guide](overlays/wiki/deployment/docker.md) |
| Upstream developer/API behavior | Existing Silo reference plus the relevant [Bloem overlay](README.md#overlays-on-upstream-documents) |
| Native API wire behavior | [Native reference](../bloem-api-reference.md), owned `internal/apiv2/bloem_*document*.go` declarations |
| Security, tenancy, storage and playback invariants | Bloem-owned files under `docs/architecture/`, listed in the [index](README.md) |
| Generated contract coverage | [API catalogue](api-catalogue.md) |
| Exact deployment evidence | Dated `docs/operations/` records with revision, checks and limits |
| Temporary implementation plan | Ignored `docs/superpowers/`; distill durable facts before committing |

Keep Silo-owned Markdown unchanged. An overlay identifies the upstream section
and the Bloem replacement or addition. The seam ledger must not grow for a
documentation refresh. Historical deployment records retain their original
facts; a current landing page must not present an old record as live health.

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

- Nullable account `max_profiles` inherits the group limit; it does not mean
  unlimited profiles. Ordinary explicit overrides and managed entitlement ceilings
  retain their separate rules. Both client releases need the
  [contract transition](../architecture/bloem-contract-adjudications.md).
- Silo's administrator CSS/font/color controls were removed. Bloem organization,
  campaign and seasonal branding remain separate features.
- Libraries can combine multiple local paths and selected roots from several
  storage sources. Preserve folder navigation and existing selections; provider
  and playback readiness remain separate from configuring a source.
- Silo's Jellyfin and Audiobookshelf compatibility servers run on their own ports;
  Bloem's companion applications and compatibility gateway are removed. See
  [protocol compatibility](../architecture/bloem-compatibility-removal.md).
- Pastime managed tracking is removed. Do not restore its API/setup guidance;
  any future ordinary plugin integration has its own contract.
- Authenticated browse-only content policy does not by itself establish a public
  showcase. Preview principal, guest sessions, catalogue hygiene and Garden launch
  integration are now implemented in their respective source repositories. Verify
  both deployed revisions, preview-host configuration and the launch/exchange
  journey before describing a live prospect flow as enabled. Native preview
  controls and later showcase presentation remain separate follow-up work.

When finishing, state the source revision, changed docs/artifacts, verification
scope and remaining gaps. A source refresh alone neither deploys a server nor
publishes a manual or adopts contracts into a client release.
