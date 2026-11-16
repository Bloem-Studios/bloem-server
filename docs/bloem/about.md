# Bloem Server in depth

Bloem Server is a self-hosted media server for films, series, audiobooks, ebooks, podcasts and manga. It is a fork of
[Silo Server](https://github.com/Silo-Server/silo-server), maintained by Bloem Studios. Most of what you run is
Silo's: the scanner, the catalog, playback (direct play, remux, hardware transcoding), the web app and the plugin
runtime. Bloem adds organizations, Live TV and DVR, storage-plugin libraries, announcements and promotions, and a
native API for its own apps.

This page covers where Bloem comes from, what it adds and removed, what you get, and how to run, configure and build
it. The [front page](../../.github/README.md) is the short version.

> [!IMPORTANT]
> **Bloem can run on a Silo database, and hand it back.** Bloem can take over an existing Silo installation in place.
> Accounts, profiles, permissions, watch history and settings written by one server are there when the other starts.
> One server runs at a time, and a switch is a stop, a start and a route change. Bloem-only features wait unused
> while Silo serves. Keep Silo on a release Bloem has already merged, because a newer one can change the schema in
> ways Bloem has not adapted to. See [switching between Silo and Bloem](../architecture/bloem-silo-switching-explained.md)
> and the [Docker guide](overlays/wiki/deployment/docker.md#switching-between-silo-and-bloem).

## Where Bloem comes from

[Silo](https://github.com/Silo-Server/silo-server) is licensed AGPL-3.0-or-later, and its trademarks belong to Silo
Media L.L.C. It is pre-1.0 and changes daily. Its 1.0 scope is movies and series, with books and Audiobookshelf
compatibility labelled beta. It ships the web app, the `/api/v2` native API, Jellyfin- and Audiobookshelf-compatible
servers on their own ports, plugin-driven metadata and distributed proxy and transcode roles. Its user manual is
[siloserver.org](https://siloserver.org/docs).

Bloem is a **tracking fork**. It keeps Silo's Git history and merges Silo's `main` as it moves, instead of copying
Silo once and drifting away. Where both change the same lines, Silo's version wins, and Bloem puts its own change back
in files of its own. To keep those merges clean, Bloem keeps Silo's module path and compatibility names
(`cmd/silo`, `SILO_*` environment variables, `X-Silo-*` headers), so existing Silo-compatible clients keep working.
The web app and artwork carry Bloem's name, as [TRADEMARK.md](../../TRADEMARK.md) requires of forks. The fork policy is
in [FORK.md](../../FORK.md), and [How Bloem keeps up with Silo](../architecture/bloem-following-silo.md) explains the
merge routine.

Bloem's own apps, `bloem-apple` and `bloem-android`, are written from scratch against the server's documented
interfaces. They are not forks of Silo's apps and are not released yet.

### Credit

The scanner, catalog, playback pipeline, plugin runtime and most of the day-to-day behaviour are Silo's own design and
work, carried forward with its licence and copyright notices intact. Bloem would not exist without Silo and the people
who build it; [Built on Silo](../../.github/README.md#built-on-silo) on the front page says how to thank them, starting
with [sponsoring Silo](https://github.com/sponsors/quick104). Live TV is an attributed adaptation of
[Prairie Server](https://github.com/Prairie-Server/prairie-server); the
[source manifest](../livetv/prairie-source-manifest.tsv) lists every adapted file.

## What Bloem adds

Each area has a plain-language overview page, with pictures, and a "For developers" section at the end. The
[documentation index](README.md) lists the rest.

- **Organizations and access policy.** One server hosts several organizations. Each has its own people, profiles,
  libraries and plans, and each viewer sees only what they are entitled to. Platform and organization administrators
  are separate, and an organization's administrators can help their own members. Reusable entitlement templates and
  bulk policy changes (up to 10,000 accounts) make policy repeatable. See
  [how organizations work](../architecture/bloem-organizations.md) and
  [how plans, previews and upgrades work](../architecture/bloem-plans.md).
- **Domains and branding.** An organization can be served on its own web addresses, under its own name, logo and
  colours, and its emails link to its own domain. A sign-in on another organization's domain is refused. See
  [organization domains and branding](../architecture/bloem-organization-domains.md).
- **Household profile credentials.** Account holders can give profiles their own sign-in credentials. A database that
  uses them cannot be switched back to Silo.
- **Live TV and DVR.** HDHomeRun and Dispatcharr tuners, authenticated Xtream live providers, XMLTV guides, live
  playback, recording rules and recordings, and a `watch_live_tv` permission. `.strm` files may point at remote web
  addresses, with private network addresses refused by default. See [how Live TV works](../architecture/bloem-live-tv.md)
  and [how `.strm` files play](../architecture/bloem-strm.md).
- **Storage sources.** Ebook, movie and audiobook libraries can read from storage plugins such as S3 as well as from
  folders. This is experimental. See [how storage sources work](../architecture/bloem-storage-sources.md).
- **Requests for books.** Audiobook and ebook requests sit beside Silo's movie and series requests. See
  [how requests work](../architecture/bloem-requests.md).
- **Announcements, promotions and seasonal presentation.** Administrators write announcements for chosen audiences,
  create promotional cards, show timed cards during playback and schedule seasonal ambience. See
  [how messages and promotions work](../architecture/bloem-messages-and-promotions.md).
- **Remote control and delivery checks.** Administrators and primary household profiles can control live sessions on
  apps that support it. Library access is checked again where bytes leave the server. See
  [how remote control works](../architecture/admin-remote-control.md).
- **Native API for Bloem's apps.** Bloem-only features live under `/api/bloem/v2`, beside Silo's `/api/v2`: richer
  title pages, server-side search, profile sign-ins and generated Kotlin and Swift contracts. See
  [how apps connect](../architecture/bloem-apps.md) and the [API by topic](api/README.md).
- **Search.** Search can combine words and meaning (embeddings), and an optional second Meilisearch can take over after
  a failover. See [how search works](../architecture/bloem-search-and-embeddings.md).
- **Plugins.** A separately maintained plugin SDK and first-party plugin set, plus a storage-provider protocol, run on
  the inherited plugin host.

The [web coverage page](../architecture/bloem-web-feature-coverage.md) records which of these have screens in the web
app. A server endpoint does not mean every client supports it.

## What Bloem removed, and why

Bloem removes its own additions when Silo comes to cover the need, or when an addition no longer earns the cost of
merging it. Each removal is forward-only. [FORK.md](../../FORK.md#what-bloem-removed) has the list with dates, and the
[changelog](CHANGELOG.md) has the detail.

- **The Jellyfin and Audiobookshelf removal, undone.** Bloem removed both in October 2026, then went back to running
  Silo's servers as Silo ships them, because carrying the removal cost hundreds of changed files at every merge. They
  run on their own ports. See [protocol compatibility](../architecture/bloem-protocol-compatibility.md).
- **Companion-application enrollment and the compatibility gateway.** Both went with the first removal and stayed out.
- **Pastime managed tracking.** It needed private triggers and direct writes into Silo's tables. Pastime may return
  only as an ordinary plugin. See [Pastime removal](../architecture/bloem-pastime-removal.md).
- **Membership-owned account policy.** Policy lives on Silo's `users` table again, which is what makes switching
  between Silo and Bloem possible.
- **A music catalog.** Silo has no music library type.
- **Presentation worker processes.** They ran only Bloem's own code, without a sandbox, so the process boundary
  isolated nothing. Promotions and ambience are now chosen inside the server.
- **Smaller items.** Lifecycle idempotency receipts, v1 socket audience tickets, a header-authenticated media gate and
  some dead code.
- **Silo's v1 API.** Silo retired it in November 2026, and Bloem's own v1 routes went with it. See
  [leaving Silo's v1 API](../architecture/bloem-leaving-silo-v1.md).

## Features

This list combines Silo's functionality with Bloem's additions. The [admin guide](../wiki/admin-guide.md) and the
[user guide](../wiki/user-guide.md) describe each in depth.

**Libraries and metadata**

- Movies, series, mixed libraries, audiobooks, podcasts, ebooks and manga, each pointing at one or more folders (or, in
  Bloem, storage-plugin locations). There is no music library type.
- Metadata from plugins such as TMDB and TVDB. Local `.nfo` files are honoured, and a fix made with *Identify*
  survives rescans.
- Library monitoring on Linux, scheduled scans and external autoscan. Network shares need scheduled scans or an
  external notifier.
- Manual and smart collections, collection templates that sync from TMDB or MDBList, and configurable home rows.
- Search across titles, people and descriptions (PostgreSQL full text, or Meilisearch), a calendar, and a page per
  person.

**Playback**

- Direct play when the device supports it, remux or transcode when it does not. Hardware acceleration (VA-API, Quick
  Sync, NVENC) is probed automatically.
- A full player: audio and subtitle selection, subtitle search and machine translation where enabled, automatic
  quality steps, chapters, skip intro, next-episode countdown and sleep timer.
- Editions of one title, audiobooks with per-book position, an EPUB/PDF/comic reader, watch together and offline
  downloads where the operator allows them.

**Households, access and requests**

- Several profiles per account, each with its own watch state, a PIN and a rating ceiling for children.
- Invitations by email or code, access groups, reusable entitlement templates, and a device list with remote sign-out.
- Requests for titles the library lacks, with an approval queue. Notifications by in-app inbox, email, Discord and
  signed webhooks, plus push on phone apps.

**Operating it**

- One `docker compose up -d` starts the stack, and everything else is set in the admin UI.
- Server roles: run everything on one machine, or split into `api`, `proxy` and `transcode` nodes.
- S3-compatible object storage for artwork and downloads, tasks and logs, a diagnostics bundle with credentials
  scrubbed, and playback history that shows direct play or transcode.
- Plugins are trusted code with the server's own access and no sandbox. See [plugin trust](../architecture/plugin-trust.md).

## Run it

Bloem runs from its public image, `ghcr.io/bloem-studios/bloem-server`, which anyone can pull. Use Silo's
multi-container Compose file, `docker-compose.advanced.yml`, not `docker-compose.yml`: that one starts Silo's own
image. You need Docker with Compose 2.24 or newer, OpenSSL and a folder of media. The
[front page](../../.github/README.md#run-it) has the commands, and the
[Docker guide](overlays/wiki/deployment/docker.md) covers everything after them: ports and HTTPS, hardware
transcoding, Meilisearch, external PostgreSQL and Redis, server roles, backups and updates.

Image tags:

| Tag | What it is |
| --- | --- |
| `latest` | The image Bloem's own production runs. It moves at most once a day. |
| A dated tag, such as `2026.11.17` | One of those releases. It never moves, so pin one to choose when you update. |
| `edge` | The newest commit that passed CI. It changes several times a day. |
| The full commit SHA | One exact build, for rollbacks. |

Set the tag in `SILO_IMAGE` in `.env`. Back up `SECRET_KEY` somewhere other than the server: it encrypts every stored
credential, and a database backup does not contain it. The web app and all native APIs answer on port 8090. Silo's
Jellyfin-compatible server uses 8096 and its Audiobookshelf-compatible server 13378 (`JF_PORT` and `ABS_PORT` move
them).

## Configure it

The only values you must set are `DATABASE_URL` and `SECRET_KEY` (the Compose stack wires the database and Redis URLs
for you). Libraries, metadata providers, transcoding and users are all managed in the admin UI after the first start.
Durable state is in PostgreSQL, Redis holds only coordination and cache data, and transcodes write scratch files
locally.

| Mode | What it runs |
| --- | --- |
| `integrated` | The default: API, web app, scanner and transcoding in one process. |
| `api` | The control role. A configured local transcode fallback can still be used. |
| `proxy` | A stream proxy node that connects to the shared database and Redis. |
| `transcode` | A worker that needs only `SILO_CORE_URL` and `SILO_NODE_KEY`, with no database or Redis. |

The bundled PostgreSQL is tuned automatically at each start (`POSTGRES_TUNE`). Switch it off with
`POSTGRES_TUNE=off` when the database is external or tuned by hand. See
[PostgreSQL auto-tuning](overlays/wiki/deployment/docker.md#postgresql-auto-tuning). The server applies pending
migrations when it starts. Bloem's own migrations are forward-only, so take a database restore point before an
upgrade. Use the Bloem binary, not Silo's, for migration status and execution, because it rewrites historical
migrations that a fresh database still replays (see the
[migration filesystem](../architecture/bloem-upstream-adapters.md#migration-filesystem)).

## Build from source

1. Install Go 1.26.11 or newer (the patch `go.mod` names), Node.js 24 or newer, pnpm 10.32.1, a C compiler with libvips
   development libraries (for CGO), PostgreSQL 18 with pgvector, Redis and FFmpeg.
2. Copy `.env.bloem.example` to `.env` and add `SECRET_KEY`, `DATABASE_URL` and `REDIS_URL`. To use the development
   services, run `docker compose up -d postgres redis`.
3. Build and run:

   ```sh
   GOWORK=off make build
   ./silo
   ```

The source build listens on `PORT` (default 8080). To build the container image instead, run
`docker build -t bloem-server:local .` and set `SILO_IMAGE=bloem-server:local`. The
[development guide](overlays/DEVELOPMENT.md) covers the dev workflow, tests, migrations and project layout. How CI and
the published image work is in [Bloem CI](ci.md).

## Documentation

- [Admin guide](../wiki/admin-guide.md) and [user guide](../wiki/user-guide.md).
- [Documentation index](README.md): every Bloem page by area.
- [Bloem native API](../bloem-api-reference.md) and [API by topic](api/README.md).
- [Integration changelog](CHANGELOG.md): each Silo merge and Bloem change.
- [FORK.md](../../FORK.md) and [CONTRIBUTING.md](../../.github/CONTRIBUTING.md).

## Licence and trademarks

Bloem Server is licensed under the **GNU Affero General Public License v3.0 or later** (`AGPL-3.0-or-later`), like
Silo. See [LICENSE](../../LICENSE).

The **Silo name, logo and wordmark are trademarks of Silo Media L.L.C.** and are not covered by the AGPL. You are free
to fork and redistribute the code, but forks must not use the Silo brand as their identity and must replace its brand
assets. Bloem Server is not Silo and is not made or endorsed by Silo's developers. [TRADEMARK.md](../../TRADEMARK.md)
sets out what is permitted, including referential use such as "compatible with Silo".
