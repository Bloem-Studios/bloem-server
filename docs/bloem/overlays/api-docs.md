# Bloem overlay: Interactive API documentation

**What this overlays.** Silo's [`docs/api-docs.md`](../../api-docs.md), a
single-section page about the bundled Swagger viewer. Bloem adds to that
section and replaces nothing. An *overlay* is a Bloem-owned page that records
Bloem's additions to, or replacements of, sections of a Silo-owned document.
The Silo file stays exactly as Silo wrote it. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem serves a second, Bloem-only API under
`/api/bloem/v1`, with its own committed OpenAPI artifact
(`contracts/api/bloem/v1/openapi.json`, since `7d9cafbca`, 2026-09-14). Silo's
viewer does not show it. Bloem wrote a first version of this page with its own
viewer work (`93af17b6e`, 2026-09-09); Silo's page arrived with Silo's API v2
migration (`a0ab8f0b7`, #1075, 2026-09-13). Bloem's remaining notes moved here in
`47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`. Silo has not changed `docs/api-docs.md` since the previous review
(Silo `ea3c10a7f`).

Index: [Bloem documentation](../README.md).

---

**Location:** section “Interactive API documentation”. **Bloem adds:**

The bundled viewer at `/api/v2/docs` loads **only** the Silo native v2 contract
from `/api/v2/openapi.json`. It does not switch to Bloem extensions when a token
or a profile header is entered. Use its local Swagger UI assets for v2; no
internet-hosted validator is required.

Bloem's separate `/api/bloem/v1` contract is committed at
[`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).
This artifact is not served at an assumed `/api/bloem/v1/openapi.json` or
`/api/bloem/v1/docs` route. To browse it with Swagger tooling, load the committed
file into a trusted local viewer. Set that viewer's server/base URL explicitly
when executing requests; native administrative writes require the appropriate
organization/platform context, not just a normal account login.

The [API catalogue](../api-catalogue.md) lists typed operations and documentation
gaps. The [native wire reference](../../bloem-api-reference.md) describes
organization, household, Live TV and chi-handler wire conventions. The generated
native artifact covers the explicit inventory operations; production mount tests
check method-level agreement for conditional groups. These sources complement
each other; the v2 viewer does not certify native route coverage.

Experimental [native-storage administration](../../bloem-api-reference.md#native-storage-administration)
is also Bloem-only: its platform/organization routes and protected capability
schema are included in the [native OpenAPI artifact](../../../contracts/api/bloem/v1/openapi.json),
separate from the Silo OpenAPI surface. The composed
server can support authorized EPUB/PDF onboarding while keeping
`backend_verified: false`; generated DTO presence and artifact approval do not
certify a provider or establish native-client adoption.

Bloem `main` at `e852f92bf` includes Silo through `3e2d306b2`. Use the
generated artifacts from the source being built, and keep
[source milestones](../CHANGELOG.md) separate from the dated
[deployment records](../../operations/). Documentation does not establish a
server's running revision, provider readiness or native-client adoption.
