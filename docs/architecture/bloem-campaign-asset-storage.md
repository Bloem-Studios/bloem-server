# Campaign and seasonal asset storage

Bloem addition to [blob storage](blob-storage.md).

The ambience asset service is separate: bootstrap supplies `S3Public`, not the
selected artwork store. Campaign cards, seasonal banners and sprites therefore
require configured public S3 for uploads. Local catalog storage or a private avatar
bucket does not satisfy that prerequisite. Without it, the registry still supports
HTTPS asset references, reports `storage_available: false` and returns `503` on upload.

Asset delivery uses the existing public content-addressed `/api/v1/ambience/assets/{ref}`
route with MIME validation, ETags and conditional `304` responses. Native platform
authoring reuses that service; see the [native API](../bloem-api-reference.md#platform-campaign-and-seasonal-authoring)
and [configured-S3 evidence](bloem-web-feature-coverage.md#acceptance-evidence-and-limits).
