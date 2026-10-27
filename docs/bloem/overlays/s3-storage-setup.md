# Bloem S3 storage notes

**What this overlays.** Silo's former `docs/s3-storage-setup.md`, which Silo
deleted in `42b39765b` (#1635, 2026-09-28) when it moved operator guides out of
the server repository. Silo's current reference for the same subject is
[blob storage](../../architecture/blob-storage.md) (`internal/blobstore`, since
Silo's `b98d9af21`, 2026-09-16), which applies to Bloem as written. This page
holds only Bloem's notes beside it. An *overlay* is a Bloem-owned page that
records Bloem's additions to, or replacements of, a Silo-owned document. Silo's
files stay exactly as Silo wrote them. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Bloem adds an artwork upload service of its own for
campaign cards and seasonal packs (`internal/ambience`, first commit
`54ace4675`, 2026-09-01). Its storage behaves differently from what this page
said until 2026-10-27. Bloem's S3 notes moved out of the Silo file into this
overlay in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`cmd/silo/bloem_main.go`, `internal/ambience`,
`internal/api/handlers/admin_ambience.go`). Since this page was last edited
(against Silo `8e2e84047`), Silo changed `blob-storage.md` seven times. The
changes include managed storage transitions, offline artwork on local storage,
trickplay previews, originals cached up to 4K, local sidecar artwork, readiness
before shutdown, and artwork uploads when `HeadObject` answers `403` (`9e19d3440`,
#2688, 2026-10-21). All of them apply to Bloem. This review corrected one stale
claim: campaign and seasonal uploads do **not** require public S3 (see below).

For installation wiring, use the [Bloem Docker guide](wiki/deployment/docker.md).
Index: [Bloem documentation](../README.md).

---

## S3 in a Bloem deployment

S3 is optional. A single-node install keeps the Assets store (artwork, branding
and the other asset blobs) on local storage by default. S3 is recommended when
several hosts serve the same catalog. Bloem uses Silo's generic S3 client, so
the bucket can be on any S3-compatible service; Silo's former guide named AWS
S3, Ceph RGW, MinIO, Garage and Cloudflare R2. Configure buckets in the admin
interface as Silo's reference describes.

Check the recorded storage identity before changing any bucket setting. Once
the first write records `artwork.storage_identity`, Silo refuses a write that
would move the store (`409 artwork_storage_locked`). An `auto` backend that
resolved to local cannot simply gain a public bucket, because that would flip
the resolution on restart. Move a locked location with Silo's managed
transition instead; see [storage identity](../../architecture/blob-storage.md#storage-identity)
and [managed transitions](../../architecture/blob-storage.md#managed-transitions).

## Campaign and seasonal uploads

The platform's **Campaigns & seasonal packs** page stores uploaded campaign
cards, season banners and sprites in Silo's **Assets** store, the same store
branding uses, under the Bloem key prefix `ambience/`. Uploads therefore work
on local artwork storage as well as on S3, where the Assets store is the public
bucket. This has been so since `f293cce0d` (2026-09-23). The public S3 client is
used only when no Assets store is configured. Only with no store at all does
the page report `storage_available: false` and refuse multipart uploads with
`503`; an HTTPS artwork URL can still be entered. Private avatar storage is
never used for these uploads. The design is in
[campaign and seasonal asset storage](../../architecture/bloem-campaign-asset-storage.md).

## Verifying storage

After configuring storage through the admin UI and applying any required
restart, verify an actual upload and image read, including the MIME type,
dimensions and ETag revalidation. A registry write or a healthy server alone
does not show that upload storage works. The
[web coverage matrix](../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits)
records disposable configured-S3 checks and their cleanup; those fixtures left
no storage override configured for other installations.
