# Bloem overlay: Interactive API documentation

Bloem additions and overrides for the upstream Silo document [`docs/api-docs.md`](../../api-docs.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Interactive API documentation”, after the paragraph beginning “# Interactive API documentation…”. **Bloem adds:**

Bloem serves this upstream Silo `/api/v2` contract alongside its separate native
`/api/bloem/v1` extensions. The [Bloem API reference](../../bloem-api-reference.md) describes
organization, platform, household and Live TV adapters; the
[web coverage matrix](../../architecture/bloem-web-feature-coverage.md) maps them to the
embedded UI. [Xtream provider creation](../../bloem-api-reference.md#post-apibloemv1livetvtunersxtream)
is a native-only endpoint with write-only credentials; it has no Silo v1/v2 alias.
The viewer below does not replace that native route/capability inventory.
Experimental [native-storage administration](../../bloem-api-reference.md#native-storage-administration)
is also Bloem-only: its platform/organization routes and protected capability
schema are included in the [native OpenAPI artifact](../../../contracts/api/bloem/v1/openapi.json),
separate from the Silo OpenAPI surface. The composed
server can support authorized EPUB/PDF onboarding while keeping
`backend_verified: false`; generated DTO presence and artifact approval do not
certify a provider or establish native-client adoption.

The October 7 source baseline is Bloem
`c87b44545228c93f909275c0b5c4fc1d6a289e1b`, including Silo through `74158b4a8`.
Use current generated artifacts for the v2 contract and the
[Bloem documentation index](../README.md) for deployment evidence and remaining
validation. The [September 19 record](../../operations/2026-09-19-xtream-deployment.md)
is a historical Xtream deployment snapshot, not the current application revision
or a guarantee that another server supports those features.
