# Bloem overlay: S3 Storage Setup

Bloem additions and overrides for the upstream Silo document [`docs/s3-storage-setup.md`](../../s3-storage-setup.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “S3 Storage Setup”. **Bloem replaces** the corresponding upstream passage with:

S3 is optional for catalog/branding artwork. A single-node install uses local artwork
storage by default; S3 is recommended for multi-node deployments and remains available
for catalog exports and other operational data. S3-compatible backends include AWS S3,
Ceph RGW, MinIO, Garage and Cloudflare R2.

Campaign and seasonal uploads are a separate service and **require public S3**.
The platform's **Campaigns & seasonal packs** page can use HTTPS artwork references
without it, but upload remains unavailable (`storage_available: false`, HTTP `503`).
Local catalog artwork and private avatar storage do not enable these uploads. Check
the selected catalog storage identity before changing public-bucket configuration:
an `auto` backend already locked to local cannot silently switch to S3. See
[artwork storage](../../architecture/blob-storage.md#storage-identity).

After configuring storage through the admin UI and applying any required restart,
verify an actual upload and image read, including MIME/dimensions and ETag revalidation.
Registry CRUD or a healthy server alone does not verify upload storage. The
[web coverage matrix](../../architecture/bloem-web-feature-coverage.md#acceptance-evidence-and-limits)
records successful disposable configured-S3 checks and cleanup; those fixtures did
not leave a storage override configured for other installations.
