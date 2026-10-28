# Bloem Server deployment — October 28, 2026: native scan throughput

Production moved three times after the
[native audiobook scans receipt](2026-10-28-native-audio-deployment.md). The
[native scan redesign receipt](2026-10-28-native-scan-redesign-deployment.md)
follows this one.

| Live from (UTC) | `main` at | Migrations | Downtime |
| --- | --- | --- | --- |
| 17:13 | `f045f1808` | 1 | 26 s |
| 17:55 | `268975631` | 1 | 14 s |
| 18:41 | `0b04c33395fb9b17f152908c3785cafcd46595a1` | 0 | under 16 s |

Each image was published by the Bloem Image workflow as
`ghcr.io/bloem-studios/bloem-server:<full commit>` after Bloem CI passed on that
commit, and ran as `bloem-server:release-<short commit>`.

## What changed

The first whole-catalog Bookwarehouse audiobook scan (about 245,000 books)
published one book every 11 to 15 seconds, and the ebook library's full listing
(about 616,000 files in two flat directories) had not finished in two days.

- `e5abfa34d` checks a scan lease without reading every directory root of the
  run. The check runs before and after each probe read, and took about 100 ms
  on the audiobook run.
- `e0d5573a7` adds `bloem_storage_scan_directories_pending_idx`, so finding the
  next pending directory no longer scans the run's completed ones.
- `2538b6e41` probes native audio in 1 MiB reads instead of 128 KiB.
- `f045f1808` lists and probes up to eight audiobook directories at once; pages
  are still applied one at a time. It also carries `456d2d3a1`, a preview fix
  from another session.
- `268975631` rejects only the book whose track cannot be read. A provider that
  no longer has a track's bytes, a revision that changed, or a probe that ran
  out of time leaves the track `unavailable`, and the next scan lists that book
  again. Bytes the probe rejects leave it `invalid` for that revision.
  Authority, plugin, provider-outage and unknown failures still stop the scan,
  as do 32 books in a row with an unavailable track. Bookwarehouse lists some
  books whose files its storage does not have, and the first such file had
  stopped the scan.
- `f02081299` starts each tool input range with a 64 KiB read that doubles up to
  the chunk size. With 1 MiB chunks, ffprobe exhausted the 128 MiB input budget
  on an m4b with 79 chapters, because it opens a range for each chapter title.
  A probe failure that stops a scan now logs the causes beneath it.
- `0b04c3339` extends a page's reach one statement per level instead of in one
  recursive statement. On the ebook location that statement hashed all 616,104
  edges for every page, taking 5.4 s per page; a page of files now takes
  4–26 ms.

See [native audiobook presentations](../architecture/bloem-native-audio-playback.md)
for the probe failure rules.

The migrations are `20261028162414_bloem_storage_pending_directory_index` (in
`f045f1808`), which creates the index, and
`20261028172627_bloem_storage_audio_unavailable_tracks` (in `268975631`), which
lets staged and kept probes be `unavailable` and indexes the kept unavailable
probes. Neither writes rows.

## Procedure and result

Before each restart:

- a container snapshot `pre-main-<short commit>`;
- a database restore point of the same name: `3BF/AB977E90`, `3BF/BC76B2E0`
  and `3C0/75A45D98`. Each standby was streaming. For the two deployments with
  migrations, the WAL segment holding the restore point was archived with no
  archive failures;
- for `268975631`, a dump of `bloem_storage_audio_staged_tracks`,
  `bloem_storage_audio_probes` and `goose_db_version`.

The restart script refused to run unless the live image was the previous
release, stopped the server in 7 to 18 seconds, applied migrations offline, and
started the new image. Each came up healthy with no restarts.

At 18:45 UTC `scanner.max_concurrent_libraries` was raised from 1 to 2 so the
ebook and audiobook library scans run together; the audiobook scan had been
queued behind the ebook scan. The setting needs a restart, and the server came
back healthy 13 seconds later.

Measured over three minutes with both scans running:

| Library | Before | After |
| --- | --- | --- |
| Bookwarehouse audiobooks | 0.1 books/s | 4.1 books/s (about 16 hours for the catalog) |
| Bookwarehouse ebooks | about 40 entries/s | about 120 entries/s |

The audiobook scan recorded 8 tracks as unavailable, all `NotFound` from the
provider, and continued. One scan lease renewal timed out under load and was
retried while the lease was current.

Not established here: a completed whole-catalog audiobook scan, or the ebook
listing's `pdf` directory.

## Rollback

All migrations are forward-only. To roll back, restore the database to the
restore point before the first deployment to undo, together with that
container snapshot, and set `scanner.max_concurrent_libraries` back to 1.
