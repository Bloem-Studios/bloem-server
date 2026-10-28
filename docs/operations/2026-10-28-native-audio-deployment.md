# Bloem Server deployment — October 28, 2026: native audiobook scans

At 14:56 UTC on October 28 production moved to `main` at
`b4234e8cf571854e54b613aa22e880cc28a155b7` (system info `b4234e8c`). It
replaced `e8fd38b55`, which another session had deployed at 14:24 UTC (container
snapshot `pre-main-e8fd38b55`); that deployment's receipt is not part of this
one. The previous receipt is [October 27](2026-10-27-main-deployment.md).
The [native scan throughput receipt](2026-10-28-native-scan-throughput-deployment.md)
follows it.

## What changed

Three changes make native audiobook libraries practical on a large
Bookwarehouse catalog (about 245,000 audiobooks):

- `741e94ba0` keeps each native audiobook track's probe per location, entry
  and revision in `bloem_storage_audio_probes`, so a later scan does not open
  and probe a track whose revision is unchanged.
- `3663a8d27` keeps change tokens on audio directories that hold only
  directories and, in an audiobook scan, carries the child directories an
  incremental listing does not report (`carried` listing mode) instead of
  listing every book again. A directory with tracks is still listed in full.
  See [native audiobook presentations](../architecture/bloem-native-audio-playback.md#publication-and-retained-authority).
- `c23f005bc` lets the web admin add a Bookwarehouse audiobook source and
  offers a Bookwarehouse source only to libraries of its own feed.

`b4234e8cf` restamps the two new migrations after `20261028150000`, which
`e8fd38b55` had already applied.

| Item | Verified value |
| --- | --- |
| Runtime image | `bloem-server:release-b4234e8cf`, published by the Bloem Image workflow as `ghcr.io/bloem-studios/bloem-server:b4234e8cf571854e54b613aa22e880cc28a155b7`, revision label matching |
| CI | Bloem CI passed all 24 jobs on `b4234e8cf` |
| Runtime configuration | identical to `release-e8fd38b55` (environment, entrypoint, command, user, ports, healthcheck, volumes) |
| Migrations | 2 applied, forward-only, zero pending |

The migrations are `20261028151500_bloem_native_audio_probe_cache`, which
creates the empty `bloem_storage_audio_probes` table, and
`20261028152000_bloem_storage_carried_directories`, which widens the
`bloem_storage_scan_directories.listing_mode` check to allow `carried`. Neither
writes rows.

## Procedure and result

Before the restart:

- container snapshot `pre-main-b4234e8cf`;
- database restore point `pre-main-b4234e8cf` at LSN `3BF/2B42EF08`; the WAL
  segment holding it was archived at 14:55:52 UTC with no archive failures,
  and the standby was streaming;
- a dump of `bloem_storage_scan_directories` and `goose_db_version`.

The restart script refused to run unless the live image was `e8fd38b55`. It
stopped the server in 6 seconds, applied both migrations offline in about a
second, and started the new image. Ready returned 200 12 seconds after the stop
began, and the server was healthy with no restarts. The runtime fingerprint did
not change.

After start:

- ready, health and the site returned 200 publicly;
- active streams continued (one FFmpeg process before, two after);
- the new table exists and the check allows `carried`;
- no panic lines; the one ERROR line was the recommendation embeddings job,
  whose Gemini embedding API still answers `402` because the provider
  account's prepaid credit is used up, as on October 27.

The Bookwarehouse ebook and audiobook sources were then upgraded to plugin
0.3.5 from the plugin catalog. Two rescans of the one-book audiobook test
library completed, the book stayed published, its probe was kept at the
track's revision, and a playback start returned `playable` with
`original_http`, streaming the same bytes as before the deployment.

Not established here: an incremental scan of a whole-catalog audiobook
library on production. Its first scan is a full listing of every book.

## Rollback

Both migrations are forward-only. To roll back, restore the database to the
restore point above together with the container snapshot.
