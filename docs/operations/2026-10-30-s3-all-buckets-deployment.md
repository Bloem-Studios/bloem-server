# Bloem deployment — October 30, 2026: guided S3 setup and all-bucket sources

Production runs `main` at `8f4ba105c8bf1c7d101f66373b195b5d61ae1a09` from 15:17 UTC,
together with S3 storage plugin 0.4.0. The owner decided on 2026-10-30 that an S3 source
may leave its bucket empty, and asked for a much simpler S3 setup for administrators who
have never configured an S3 endpoint. They authorized building, releasing and deploying
both pieces without waiting for CI.

## What changed

- **S3 plugin 0.4.0** (`Bloem-Studios/bloem-storage-s3` `55b81f8`, tag `v0.4.0`). A
  source without a bucket covers every bucket its keys can list: each bucket is a
  top-level folder, paths are `bucket/key`, and a prefix is refused without a bucket.
  Sources that name a bucket keep their namespace and entry IDs (pinned by a golden test).
  Built with Go 1.27.3. Releases `v0.4.0` and catalog `s3-v0.4.0`. All eight public
  assets were checked against the local SHA-256 values. Catalog `plugins.json` lists
  0.4.0. linux/amd64 `e289d6a3a19039f21f978de075f41fe856ef49a10c95c1c55455a8efa1122ff4`.
- **Web** (`38d233cf0`..`8f4ba105c`). The Add S3 form asks for the provider first
  (Backblaze B2, Wasabi, Cloudflare R2, Amazon S3, DigitalOcean Spaces, Hetzner or Other).
  It derives the endpoint and signing region from what that provider calls its region,
  datacenter or account ID, names the keys the provider's way, and explains where to find
  them. With a 0.4.0 release, connecting defaults to "All buckets these keys can see
  (recommended)": one source with no bucket. The edit form labels Prefix and Session token
  as optional and says the keys must be entered again. The library folder picker names
  sources by their source name, lists an all-bucket source's buckets as folders, counts
  folders across buckets, and warns that choosing "All buckets" includes buckets added
  later. See [native storage](../architecture/bloem-native-storage.md#s3-all-buckets-sources).
- No Go code, API contract or migration changed.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels. The web bundle was built from the same
  commit. Tagged `bloem-server:release-8f4ba105c` and copied with `docker save | docker
  load` to CT135 and to the transcode node CT172.
- **Approvals.** The host admits S3 catalog releases only when their digest is in the
  static approvals file. The `s3-0.4.0-linux-amd64-e289d6a3a190` entry (the release's
  `manifest.json` object and checksum) was added to the host's approvals file before the
  restart. The previous file is kept with suffix `.bak-20261030-pre-s3-0.4.0`.
- **Before the restart:** container snapshot `pre-main-8f4ba105c`; database restore point
  of the same name at LSN `3E7/A414FCF8`. Segment `00000003000003E7000000A4`, which holds
  it, was archived at 15:16:47 with no archive failures.
- **Restart.** The restart script refused to run unless the live image was `6d6fbf1b5`.
  The stop began at 15:16:59 and took 38 seconds. No migrations applied, and 739 stay
  applied. The server listened at 15:17:45, about 47 seconds after the stop began, and
  was healthy with no restarts. The script's printed timings were not kept; these times
  come from Docker's events and the server log.
- The runtime fingerprint is unchanged apart from the image. The binary's SHA-256 is
  `31a845f31069f263633e12e8620be459a7d89470020af71a6017b5a60ff14b68`, and
  `/api/v2/system/info` reports `8f4ba105`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-8f4ba105c` and are healthy.
- **S3 installations 33 and 35** were upgraded to the 0.4.0 catalog release through the
  platform admin API. Their root entry IDs are unchanged, and both browse as before:
  "Movies" lists the bucket's top-level folders, and "S3 Movies Test" lists its
  single-title prefix.

A bucket-less source was also exercised against a local Garage server with two buckets:
both appeared as folders, nested folders listed, and each file read from its own bucket.
Creating one in production needs S3 keys that can list buckets, and those are entered by
the owner in the Add form.

To roll back, start `bloem-server:release-6d6fbf1b5` again (no migrations ran) and
downgrade the two installations to `s3-0.3.2`. The 0.3.2 approval entry stays in the
file. A 0.4.0 bucket-less source has no 0.3.2 equivalent; disable it before downgrading.
