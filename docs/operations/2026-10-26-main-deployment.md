# Bloem Server deployment — October 26, 2026

This receipt records the deployment of `main` after the Silo merges through
`6de6d5e5e`. Production had run `2393c41ee` since October 18; nothing had been
deployed since, so the 21 Silo migrations those merges brought were pending.

## Deployed source and artifacts

| Item | Verified value |
| --- | --- |
| Application source on published `main` | `d183b286f299c6009d91ec9fce53972ddf343064` |
| System-info server version | `d183b286` |
| Previous application source | `2393c41ee` |
| Runtime image | `bloem-server:release-d183b286f` |
| Server binary SHA-256 | `593eb5fd54623f1b40a7b7113c729529e762da7b9c8195b482e0bd28ea501424` |
| Promotions worker SHA-256 | `0b51fda3ab179d9c052a13156209224df50fd987bb0ff361dbfa3f8842492ab7` |
| Ambience worker SHA-256 | `d06c454fd63bb74870420bf8e5c7e6d15cd4ca0f8f349f20bb7a5289a38f1a14` |
| Migrations applied in this deployment | 21, all Silo's; zero pending |
| Applied migration count | 719 |
| Maximum migration version | `20261024034851` |

## Build

The image was built from a `git archive` of the source with the repository
Dockerfile, injecting the web bundle as the `frontend_dist` build context, as
CI does. The Dockerfile's own frontend stage cannot build this tree: web tests
import `docs/design/schemas/buffering-quality/v1/vectors.json`, which that
stage does not copy. Silo's Dockerfile has the same gap. CI could not build the
image: Silo's `TestLogPartitionsMatchPartmanOnNonUTCServer` fails on every
branch while its 8-day window crosses the 2026-11-01 US daylight-saving change.

## Procedure and result

Before the restart: a container snapshot, a database restore point, and a dump
of the plugin, native storage and server settings tables. WAL archiving was
current. The script refused to run unless the live image was the previous
release. It stopped the server cleanly in 36 seconds, ran the migrations
offline in 3 minutes 12 seconds, and started the new image; the server was
ready 234 seconds after the stop began, with no restarts.

The runtime configuration matched the previous container except for one
image default, `NODE_OPTIONS=--use-openssl-ca`, from Silo's Dockerfile. Every
mount, including the native storage approval map, was retained.

After start: ready and health returned 200 locally and publicly, both
presentation workers were running with the hashes above, the three native
storage installations were enabled and unchanged, and there were no ERROR or
panic log lines.

## Included changes

Besides the Silo merges: profile and member password hashing under Silo's
admission limit, the v1 sockets on Silo's tickets, and listing pages getting
their 30-second deadline from the storage plugin session. The Bookwarehouse
plugin 0.3.1 had been upgraded separately the same night.
