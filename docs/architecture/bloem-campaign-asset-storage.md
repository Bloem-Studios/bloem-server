# Campaign and seasonal asset storage

> **Provenance.** Bloem addition to Silo's [blob storage](blob-storage.md). The
> ambience (seasonal pack) registry and its artwork service are Bloem's
> (`internal/ambience`, first commit `54ace4675`, 2026-09-01); campaign cards share it.
> This page was written in the Silo merge `64bbab66b` (2026-09-23), which brought in
> Silo's blob store and its `blob-storage.md`. Later the same day `f293cce0d` moved the wiring into
> `cmd/silo/bloem_main.go` and switched the service from the public S3 client to the
> catalog Assets store, which the first version of this page did not reflect.
> **Status:** current at `e852f92bf`.

The ambience asset service stores campaign cards (`campaign_card_16x9`), seasonal
banners (`season_banner`) and sprites (`season_sprite`) in Silo's **Assets** blob
store, the same store branding uses, under the key prefix `ambience/`. That prefix is
Bloem's; Silo's prefix table does not list it. Uploads therefore work on local artwork
storage as well as on S3, where the Assets store is the public bucket.
`bloemWireServices` (`cmd/silo/bloem_main.go`) falls back to the public S3 client only
when no Assets store is configured.

With no store at all, the registry reports `storage_available: false`, multipart
uploads return `503`, and an HTTPS artwork URL can still be authored. A successful
registry write does not prove that an asset was uploaded.

Uploads are content-addressed: the server sniffs the bytes (the declared type is
ignored), accepts PNG, JPEG, WebP and GIF, and names the object after the SHA-256 of
its content. Delivery uses the public `/api/v1/ambience/assets/{ref}` route
(`mountBloemPublicV1` in `internal/api/router_bloem_wiring.go`), with
`X-Content-Type-Options: nosniff`, an immutable cache policy, an ETag equal to the
reference and conditional `304` responses. It is public because seasonal artwork can
render on the login screen. Native platform authoring reuses that service; see the
[native API](../bloem-api-reference.md#platform-campaign-and-seasonal-authoring).

The browser acceptance recorded in
[web feature coverage](bloem-web-feature-coverage.md#acceptance-evidence-and-limits)
(September 18, 2026) ran against configured public S3, before the switch to the
Assets store. No later upload acceptance against local artwork storage is recorded.
