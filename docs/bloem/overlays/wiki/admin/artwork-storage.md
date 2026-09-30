# Bloem artwork storage

Read the upstream [blob-storage architecture](../../../../architecture/blob-storage.md)
for catalog and avatar storage. The former upstream wiki page has been removed;
the additions below describe Bloem's separate campaign/seasonal upload service.
Index: [Bloem documentation](../../../README.md).

## Campaign and seasonal artwork

Uploads in **Campaigns & seasonal packs** require a configured public S3 bucket.
They do not use local catalog artwork or private avatar storage. Without public S3,
you can still enter an HTTPS artwork URL, but uploads are unavailable. This does not
prevent initial server setup or local catalog artwork. See
[S3 storage setup](../../s3-storage-setup.md) before changing storage configuration;
the catalog backend lock described in the upstream reference still applies.
