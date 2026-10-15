# Bloem overlay: Interactive API documentation

Bloem additions and overrides for the upstream Silo document [`docs/api-docs.md`](../../api-docs.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

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

The October 14 source integration includes Silo through `ea3c10a7f`.
Use the generated artifacts from the source being built and keep
[source milestones](../CHANGELOG.md) separate from
[historical deployment records](../README.md#operations-and-deployment-records).
Documentation does not establish a server's running revision, provider readiness
or native-client adoption.
