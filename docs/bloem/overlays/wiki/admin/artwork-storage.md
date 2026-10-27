# Bloem artwork storage

**What this overlays.** Silo's former operator page
`docs/wiki/admin/artwork-storage.md`, which Silo deleted in `42b39765b` (#1635,
2026-09-28) along with the rest of its in-repository wiki. Silo's current
reference for catalog, branding and avatar storage is
[blob storage](../../../../architecture/blob-storage.md), which applies to Bloem
as written. This page adds only Bloem's campaign and seasonal artwork. An
*overlay* is a Bloem-owned page that records Bloem's additions to, or
replacements of, a Silo-owned document; Silo's files stay exactly as Silo wrote
them. See the [documentation workflow](../../../documentation-workflow.md).

**Why Bloem differs.** Campaign cards and seasonal packs are Bloem features
(`internal/ambience`, since `54ace4675`, 2026-09-01). Bloem's note moved out of
the Silo page into this overlay in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`cmd/silo/bloem_main.go`). This review corrected the earlier claim
that these uploads require a public S3 bucket. They have used the catalog
Assets store since `f293cce0d` (2026-09-23).

Index: [Bloem documentation](../../../README.md).

---

## Campaign and seasonal artwork

Uploads in **Campaigns & seasonal packs** go to the same Assets store as
catalog artwork and branding, under the key prefix `ambience/`. With local
artwork storage they are written to the local artwork root; with S3 they go to
the public bucket. Only when no Assets store and no public S3 client are
configured are uploads unavailable (`storage_available: false`, multipart upload
`503`). Even then you can still enter an HTTPS artwork URL, and initial server
setup and catalog artwork are unaffected. Private avatar storage is never used.

Silo's storage lock applies to these uploads too, because they share the
Assets store. Read [S3 storage notes](../../s3-storage-setup.md) and Silo's
[storage identity](../../../../architecture/blob-storage.md#storage-identity)
before changing storage configuration. Design details are in
[campaign and seasonal asset storage](../../../../architecture/bloem-campaign-asset-storage.md).
