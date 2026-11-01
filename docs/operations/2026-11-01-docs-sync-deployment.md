# Bloem deployment — November 1, 2026: production level with `main` after the documentation rewrite

Production has run `main` at `ab4aadc7566c9ae0a9fa902af09008193cdd0377` since 14:34:26 UTC.
It replaces `59616c4af`. Everything in between is documentation, code comments and picture
scripts: the house-style rewrite of every Bloem page, the removal of stale pages, and the
three specs folded into current pages. The only change compiled into the server is the
description text of the embedded client registry schema
(`contracts/client/v1/registry.schema.json`). Behaviour is unchanged; the owner asked for
production and both transcode nodes to run the current `main`.

Silo `main` was still at `63ef34b23`, already merged. The image was built locally without
waiting for CI.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, as for `8a721cff2`. Tagged
  `bloem-server:release-ab4aadc75` and loaded on CT135 and CT172. The binary
  `/usr/local/bin/silo` has SHA-256
  `76281e2287e35f31f59e4ef1ac734ecec3d5986a7bc0febeaf7d6e5832a28dfa` on both hosts.
- **Before the restart:** container snapshot `pre-main-ab4aadc75`. Database restore point of
  the same name at LSN `42C/C2E18930`. Segment `000000030000042C000000C2`, which holds it, was
  archived after a WAL switch, with no archive failures.
- **Restart.** The kit's restart script required the live image to be `59616c4af`. One
  transcode was running, and the stop took 7 seconds (exit 0). No migrations were pending;
  750 are applied. The server answered ready at 14:34:30, 13 seconds after the stop began,
  and was healthy with no restarts. `/api/v2/system/info` reports `ab4aadc7`;
  `organization_domains_v1` is listed and `GET /api/bloem/v1/ambience/public` answers `200`.
  No error lines were logged after the start.
- **Transcode nodes.** CT172's node was idle and moved at once to `release-ab4aadc75`,
  healthy. CT135's node was serving one viewer a conversion that had run for over two hours;
  Silo's drain lets a stream finish on its node rather than moving it, and the owner chose to
  restart it at once. It stopped at 14:42:20, reported draining with one job, and was healthy
  on `release-ab4aadc75` at 14:42:36. The viewer's player resumed within seconds: a new
  conversion started on the restarted node, which the server lists as healthy with one job,
  and segment and progress requests continued. Their previous compose files are kept with
  suffix `.bak-pre-ab4aadc75`.

## Rollback

Start `bloem-server:release-59616c4af` again and set the transcode nodes back from their
`.bak-pre-ab4aadc75` compose files. No schema changed.
