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
The viewer below does not replace that native route/capability inventory. The
[deployment record](../../operations/2026-09-19-xtream-deployment.md) describes the deployed
revision and validation limits, not a guarantee that another server supports it.
