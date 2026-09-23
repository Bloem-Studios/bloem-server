# Bloem overlay: Artwork storage

Bloem additions and overrides for the upstream Silo document [`docs/wiki/admin/artwork-storage.md`](../../../../wiki/admin/artwork-storage.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../../README.md).

**Location:** section “The backend is fixed once artwork is stored”, after the paragraph beginning “Profile avatars remain in private S3 when configured. Otherwise they…”. **Bloem adds:**

## Campaign and seasonal artwork

Uploads in **Campaigns & seasonal packs** require a configured public S3 bucket.
They do not use local catalog artwork or private avatar storage. Without public S3,
you can still enter an HTTPS artwork URL, but uploads are unavailable. This does not
prevent initial server setup or local catalog artwork. See
[S3 storage setup](../../../../s3-storage-setup.md) before changing storage configuration;
the catalog backend lock above still applies.
