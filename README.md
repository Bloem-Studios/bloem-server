<p align="center">
  <img src="assets/bloem-banner.png" alt="Bloem Server" width="520" />
</p>

# Bloem Server

Bloem Server is a public tracking fork of
[Silo Server](https://github.com/Silo-Server/silo-server), maintained by
Bloem Studios. It retains upstream Git history, module paths, protocol
identifiers, and environment-variable names so upstream commits remain
mergeable and existing Silo-compatible clients keep working against it. See
[FORK.md](FORK.md) for provenance, deliberate divergence, and the
protected-upstream remote setup.

The operational documentation below follows upstream closely, since most of
it — deployment, configuration, self-hosting — applies identically to both
projects. Technical names such as `SILO_DATA_ROOT` remain compatibility
contracts; the distributed web application and visual assets identify the
product as Bloem, per [TRADEMARK.md](TRADEMARK.md)'s rebranding requirement
for forks.

## Credit where it's due

Bloem Server exists because [Silo Server](https://github.com/Silo-Server/silo-server)
does. The scanner, catalog, playback pipeline, plugin runtime, and the large
majority of this codebase's day-to-day behavior are Silo's own design and
implementation, licensed AGPL-3.0-or-later, carried forward here with its
license and copyright notices intact — see [LICENSE](LICENSE) and
[FORK.md](FORK.md). If you find this project useful, the credit — and any
support you'd like to give — belongs first to the people building Silo
itself: <https://github.com/sponsors/quick104>.

Live TV, OTA/DVR, and EPG support is a separate attributed AGPL adaptation of
the [Prairie Server](https://github.com/Prairie-Server/prairie-server)
subsystem; see `docs/livetv/prairie-source-manifest.tsv` for the pinned
source this was adapted from.

Bloem tracks Silo's `main` closely rather than in occasional large batches: an
automated daily job attempts the upstream merge, opens a pull request when it
merges cleanly, and only auto-merges once CI is green. A conflict — judgment
about the Bloem delta — is left open for manual review instead of being
forced through. Day-to-day upstream work (playback, the web app, metadata,
and everything else covered above) reaches Bloem promptly this way; recent
examples include per-client artwork-size negotiation and playback-startup
latency hardening, both upstream Silo features.

## What Bloem adds on top of Silo

This fork stays close to upstream deliberately (see FORK.md for why), and
adds a focused set of its own capabilities alongside it:

- A tenant and identity foundation under `/api/bloem/v1` — organization lifecycle
  and membership management (with an admin UI for people/security
  administration), organization-scoped profile groups, a separate
  administrative-context session system, and policy-bounded visibility of
  organization-owned or explicitly entitled media folders — while `/api/v1`
  remains the Silo-compatible projection. It has documented, reviewed Bloem
  exceptions; see [the v1 compatibility policy](docs/architecture/v1-scope.md) and
  [the operator runbook](docs/architecture/opa-tenant-authorization.md).
- Revisioned **entitlement templates** for consistently applying playback,
  stream, profile, transcode, download, request, permission, quality, and
  library policy to an organization or a directly managed account. Bloem
  ships Browse-only, Viewer, Standard, Premium, and Reseller Member starting
  points; platform admins can create, revise, clone, archive, preview, and
  apply templates from the web console. See the
  [entitlement-template operations guide](docs/operations/entitlement-templates.md).
- Immutable **policy cohorts** for safely moving reviewed sets of existing
  accounts between exact template revisions, selection-specific derived
  policies, and the managed default. Operators can preview profile impact,
  run restart-safe jobs, and reconcile the result against authoritative
  account and profile policy reads. See the
  [bulk policy administration guide](docs/operations/bulk-policy-cohorts.md)
  and [release/canary runbook](docs/operations/bulk-policy-cohorts-runbook.md).
- A companion-deployment gateway: enrollment, trust, and administration for
  companion instances running behind this server, with its own hardened
  default-deny posture.
- Optional direct profile login and shared-device pairing, for households
  that want a profile to sign in without a full account credential each
  time.
- A native client API surface — richer Watch documents (cast/crew, chapter
  and skip-intro markers, file editions, server-side search, poster
  resolution), a person-detail endpoint, and a batch-resolved
  similar-items endpoint — built to serve Bloem's own native Android/Apple
  clients, verified against real contract-conformance and install/scan
  acceptance test suites (`internal/clientcontract`, `internal/acceptance`).
- A private plugin SDK, catalog, and first-party plugin set.
- Product identity: Bloem naming and branding in user-facing copy, applied
  at build time so upstream source stays mergeable (see FORK.md).

Unless called out above, the feature list below, deployment, and configuration
remain Silo's own work, unchanged.

## Features

Everything below is described in more depth in the
[admin guide](docs/wiki/admin-guide.md) and the [user guide](docs/wiki/user-guide.md).

**Libraries and metadata**

- **Plays your media, your way** — direct play when the device supports it, remux or hardware-accelerated transcode (including NVENC) when it doesn't.
- **Every kind of library** — movies, series, music, audiobooks, books and comics, each pointing at one or more folders; a first-run wizard creates the admin account and the first library.
- **Plugin-driven metadata** — match and enrich your libraries with providers like TMDB and TVDB, installed as plugins; local `.nfo` sidecar files are honoured and merged with provider data; a wrong match is fixed with *Identify* and the fix survives rescans.
- **Autoscan** — watch media folders and scan only what changed, per library, with a scan interval for network shares where folder watching does not fire.
- **Collections and home sections** — manual collections, rule-based smart collections, and collection templates that sync from TMDB, Trakt or MDBList; the admin chooses which rows the home screen offers, viewers hide and reorder them.
- **Search, calendar, people** — search across titles, people and descriptions (optionally backed by Meilisearch), a calendar of upcoming episodes and releases, and a page per actor, director or writer.
- **Per-profile watchlist, favourites and history**, with the option to remove entries or mark something unwatched.

**Playback**

- **A full player on every device** — audio and subtitle track selection, subtitle appearance settings, subtitle search and machine translation where the operator enables it, an automatic quality ladder, chapters, skip intro, next-episode countdown and a sleep timer.
- **Editions** — a title that exists in more than one version (theatrical and extended, two languages) lets the viewer choose which to play.
- **Music, audiobooks and reading** — a music queue with shuffle and repeat; audiobooks with per-book position, speed control, chapters and a sleep timer, with lock-screen controls; an EPUB/PDF/comic reader whose position follows the reader across devices.
- **Watch together** — several people on different devices watch the same thing in sync with a shared pause, across the web app and the phone and TV apps.
- **Offline downloads** to phones and tablets when the operator allows it, per device, with separate controls for original and transcoded downloads.
- **Hardware acceleration that probes itself** — set it to *auto* and Bloem checks what the container can actually reach, falling back to software; VA-API/Quick Sync and NVIDIA come as Compose overlays.

**Households, accounts and access**

- **Household profiles** — multiple profiles per account, with per-profile watch state and parental controls: a PIN per profile and a rating ceiling for children.
- **Invitations and invite codes** — invite by email (no account exists until it is accepted) or hand out codes with a use limit and a policy.
- **Access groups** — named permission sets (download, request media, Live TV) attached to users; deleting a group moves its members to the default group, never leaving anyone without a policy.
- **Reusable access policy** — immutable entitlement-template revisions and organization policy cohorts make reviewed policy changes repeatable for one account or up to 10,000 snapshotted accounts, with separate controls for original downloads and transcoded downloads and either all libraries or an explicit library selection.
- **Devices** — every signed-in phone, TV and browser, with remote sign-out, for the operator and for each viewer; TVs can sign in with a code from the phone app.

**Requests, notifications and Live TV**

- **Requests** — viewers ask for titles the library does not have; the operator approves, declines or fulfils from a queue, and the requester is notified when the item appears.
- **Notifications** — an in-app inbox, email, Discord, and HMAC-signed generic webhooks whose receivers are disabled automatically when they keep failing; push notifications on the phone apps.
- **Live TV, guide and DVR** — tune, record and show a programme guide from HDHomeRun tuners and Dispatcharr, discovered on the LAN or probed by address, with XMLTV guide sources and a Compose override for host networking. A separate, attributed adaptation of Prairie Server's subsystem (see above).

**Compatibility and clients**

- **Web app included** — a full-featured web client and admin interface ship with the server.
- **Works with apps you already use** — a Jellyfin/Emby-compatible API supports clients such as VidHub, Findroid, and Infuse, and an Audiobookshelf-compatible API supports Audiobookshelf-protocol clients for audiobook/podcast playback, progress sync, bookmarks, and RSS feeds. Both are enabled by default and reachable on Bloem's own address — no extra ports to open. Both can be turned off in Admin > Settings, and an operator who wants a dedicated listener on a fixed port (`JF_PORT`/`ABS_PORT`, `8096`/`13378`) can still opt into one there.
- **Native Bloem apps** for phone, tablet and TV, served by the native client API described above.
- **Watch sync** to and from outside services, and optional **AI-assisted features** such as description translation, each with its own provider key.
- **Themes and accessibility** — an operator-chosen default look, a theme editor for viewers on the web app, larger text, reduced motion and high contrast.

**Operating it**

- **Fast setup** — one `docker compose up -d` brings up the whole stack; everything else is configured in the admin UI.
- **Server roles** — run everything on one machine (`integrated`) or split into `api`, `transcode` and `proxy` nodes sharing one database, with a Nodes page showing each worker's GPU, scratch disk and load.
- **Object storage** for artwork and downloads on S3, MinIO or Cloudflare R2.
- **Tasks, logs, diagnostics, maintenance and stats** — every background job with progress and history, a filterable server log, a one-click diagnostics bundle with credentials scrubbed, safe housekeeping tasks, and playback history that shows whether each play was direct or transcoded.
- **Self-migrating updates and PostgreSQL auto-tuning** — the database migrates itself on start, a pinned image is one `SILO_IMAGE` line away, and the bundled PostgreSQL is tuned to the host (see Configuration below).
- **Plugins are installed by the host operator**, not from the web app — a deliberate security boundary.

## Quick start

The shortest path to a running server, from the [admin guide](docs/wiki/admin-guide.md).
You need Docker with Compose 2.24+, Git and OpenSSL, and a folder of media.

```sh
git clone https://github.com/bloem-studios/bloem-server.git
cd bloem-server
cp .env.example .env
chmod 600 .env
printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
```

Back up `SECRET_KEY` somewhere that is not the server; it encrypts every stored
credential and a database backup does not contain it. Then open `.env` and set
the absolute path to your media:

```dotenv
MEDIA_ROOT=/path/to/your/media
```

```sh
docker compose up -d
```

Open **http://localhost:8090** (or `http://<server-ip>:8090` from another
machine). The setup wizard creates the administrator account, confirms the
server's address and adds the first library; scanning starts right after. For
hardware transcoding, invite flows, reverse proxies and everything else, keep
reading or go straight to the [admin guide](docs/wiki/admin-guide.md).

## Deploy with Docker (recommended)

The easiest way to run Bloem is with Docker Compose 2.24 or newer. The default stack assumes you do
not already have PostgreSQL and Redis available, so it bundles PostgreSQL, Redis, FFmpeg, and the
application for a one-command start. It pulls
`ghcr.io/bloem-studios/bloem-server:latest` by default; `SILO_IMAGE` remains
the Compose override name for upstream-configuration compatibility.

1. **Create a `.env` file**

   ```sh
   cp .env.example .env
   printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
     "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
   ```

   This replaces the development database password from `.env.example` and creates the key Bloem
   uses to encrypt stored credentials. Back up `.env` separately from PostgreSQL; losing
   `SECRET_KEY` makes those credentials unrecoverable.

2. **Set your media path**

   Edit `.env` and set:

   ```dotenv
   MEDIA_ROOT=/path/to/your/media
   ```

   `MEDIA_ROOT` is the one value most users need to change. You can also override `SILO_DATA_ROOT` if you do not want bind mounts under `/opt/silo`, and change ports if the defaults conflict with something else on the host. `SILO_*` names are retained compatibility identifiers.

3. **Start the default integrated stack**

   ```sh
   docker compose up -d
   ```

   This starts PostgreSQL, Redis, and the integrated Bloem server. The app is available at `http://localhost:8090`. Jellyfin/Emby-compatible app support and Audiobookshelf-compatible app support are both enabled by default, reachable on that same address — no extra ports needed. Either can be turned off from Admin > Settings if you don't need it, and an operator who wants a dedicated listener on a fixed port instead can opt into one there too.

   If you already have PostgreSQL and Redis available, omit those bundled service examples from compose and point Bloem at your existing `DATABASE_URL` and `REDIS_URL` instead.

   ### Optional Intel/AMD VA-API or Intel Quick Sync

   The default stack is CPU-only so it starts on hosts without `/dev/dri`. On a Linux host with
   `/dev/dri`, enable the device overlay:

   ```sh
   docker compose -f docker-compose.yml -f docker-compose.vaapi.yml up -d
   ```

   To make that the default for this installation, set:

   ```dotenv
   COMPOSE_FILE=docker-compose.yml:docker-compose.vaapi.yml
   ```

   ### Optional NVIDIA/NVENC

   GPU support is kept out of the default compose file so hosts without NVIDIA drivers work unchanged.

   Install the NVIDIA Container Toolkit and use a Docker Compose version with GPU reservation support before enabling this override.

   Use the optional override file when you want NVENC:

   ```sh
   docker compose -f docker-compose.yml -f docker-compose.nvidia.yml up -d
   ```

   If you want this controlled from `.env`, set `COMPOSE_FILE`:

   ```dotenv
   COMPOSE_FILE=docker-compose.yml:docker-compose.nvidia.yml
   NVIDIA_GPU_COUNT=1
   ```

   Windows uses `;` instead of `:` between compose files.

   Then `docker compose up -d` will include the NVIDIA override automatically.

4. **Configure through the admin UI**

   Add libraries, users, metadata providers, and playback settings from the web interface.

### Bind Mount Layout

The deploy-oriented compose files use host folder mappings rather than Docker-managed volumes.

By default, data is stored under `/opt/silo`:

- `/opt/silo/postgres`
- `/opt/silo/redis`
- `/opt/silo/plugins`
- `/opt/silo/compat`
- `/opt/silo/transcode`
- `/opt/silo/catalog-seeds`

The optional `search` profile also stores its index under `/opt/silo/meilisearch`.

Media is mounted into the container at `/mnt/media` from the host path you set in `MEDIA_ROOT`.

### Optional Search Profile

PostgreSQL full-text search works without any optional services. Meilisearch is available when you
want its search provider:

| Profile | Command | Description |
|---|---|---|
| default | `docker compose up -d` | Integrated server plus bundled PostgreSQL and Redis |
| `search` | `docker compose --profile search up -d` | Add the optional Meilisearch service |

Before starting the `search` profile, set `MEILI_MASTER_KEY` in `.env` to the output of
`openssl rand -hex 32`. After Bloem starts, choose Meilisearch under **Admin > Settings > Search**,
set the URL to `http://meilisearch:7700`, enter the same key as the API key, test the connection,
and save. Restart Bloem, then rebuild the catalog search index from the same page. Bloem continues
to use PostgreSQL full-text search until you select Meilisearch.

### Distributed Examples

The main Compose file includes commented proxy and transcode service examples. Most single-host
installs should leave them commented because the integrated service already includes proxying and
transcoding.

Multi-host operators can use those examples as a starting point for a dedicated worker Compose
file connected to the deployment's shared PostgreSQL and Redis services.

Proxy nodes serve source downloads from the same absolute media paths used by direct playback.
Prepared-download work can also run on transcode nodes. Each selected transcode node retains its
result on node-local storage and exposes it only through Silo's authenticated internal artifact API;
the paired proxy relays those bytes, so no shared artifact mount is required. Dedicated transcode
nodes default to retaining prepared downloads in a protected directory inside the transcode volume
captured at process startup. `download.artifact_dir` overrides that location for both dedicated
transcode nodes and the integrated/API-local fallback, so mount the configured path on every process
that prepares downloads. Changing either artifact-path setting requires a restart. Downloads with a
configured server-wide or per-user bandwidth limit remain API-local so those aggregate limits stay exact.
Clients discover distributed delivery through `proxy_delivery` on the download capability response.
When it is true, they may opt into `GET` or `HEAD /api/v1/downloads/{id}/file-proxy` and
`/api/v1/direct-download-proxy`; those routes may return a temporary redirect to a proxy node. The
established `/file` and `/direct-download` routes keep serving bytes directly with their existing
status-code contract; for a node-local prepared artifact, the API itself performs the authenticated
relay on that fallback route.

### Deployment Notes

The default compose stack intentionally bundles PostgreSQL and Redis for ease of setup and assumes a fresh install without those services already available. If you already operate PostgreSQL and Redis, omit those examples from compose and point Bloem at your existing infrastructure instead. For serious installs, PostgreSQL is better on a separate VM or a managed service so upgrades, tuning, and backups are isolated from the app host. Redis can stay local for many installs, but externalizing it is also reasonable if you already operate shared infrastructure.

Bloem is externally stateful by default rather than fully stateless. Durable application state lives in PostgreSQL. Redis only stores coordination and cache-style data. Bloem still writes transient transcode output locally under `/tmp/silo-transcode`. If you switch `userdb.backend=sqlite`, Bloem also becomes locally stateful at `/var/lib/silo/userdb`.

Migrating an existing Continuum Docker install should be done with the preflight
helper and cutover guide in [docs/continuum-to-silo-docker-migration.md](docs/continuum-to-silo-docker-migration.md).

## Configuration

Bloem requires `DATABASE_URL` and `SECRET_KEY` when running from source or against external
infrastructure. In the default Docker Compose path, the stack wires the database and Redis URLs
for you. All other settings — libraries, metadata providers, transcoding, users — are managed
through the admin UI after first launch.

### Server Modes

| Mode | Description |
|---|---|
| `integrated` | Full server: API + frontend + scanner + transcode (default) |
| `api` | API server only, no local transcoding |
| `proxy` | Stream proxy node that connects to the shared deployment database and Redis |
| `transcode` | HLS and prepared-download worker node that connects to the shared deployment database and Redis |

### PostgreSQL Auto-Tuning

The default Docker Compose stack does not require a checked-in `postgresql.conf`.
It enables Silo's [pgtune](https://github.com/le0pard/pgtune)-style OLTP tuning
by default:

```yaml
POSTGRES_TUNE: auto
```

When enabled, Silo connects with `DATABASE_URL` and applies recommendations with
`ALTER SYSTEM`, which writes to PostgreSQL's `postgresql.auto.conf` inside the
database data directory. Reloadable settings are applied immediately with
`pg_reload_conf()`. Settings that PostgreSQL marks as restart-only are written
too, and Silo logs the setting names so you can restart PostgreSQL once:

```sh
docker compose restart postgres
```

The default Compose database user has the required PostgreSQL permissions. If
you use an external PostgreSQL server, make sure the configured `DATABASE_URL`
user can run `ALTER SYSTEM`, or set `POSTGRES_TUNE=off` and manage
PostgreSQL yourself.

For `POSTGRES_TUNE_MEMORY=auto`, Silo uses the first trustworthy memory source:
a finite Docker cgroup limit, the read-only `/host/proc/meminfo` mount supplied
by the bundled Compose file, then `/proc/meminfo` with container safety guards.
Auto-detected memory is treated as a PostgreSQL budget, defaulting to 75% of
detected RAM so Silo, Redis, plugins, transcodes, and the OS retain headroom.
`POSTGRES_TUNE_DB_SIZE=auto` queries `pg_database_size(current_database())` and
classifies the workload by comparing the database size to that memory budget.

Optional tuning overrides:

| Variable | Default | Description |
|---|---:|---|
| `POSTGRES_TUNE_PROFILE` | `oltp` | Tuning profile. Only `oltp` is currently supported. |
| `POSTGRES_TUNE_MEMORY` | `auto` | Server/container RAM, such as `8GB` or `32GB`; explicit values are used as-is. |
| `POSTGRES_TUNE_MEMORY_BUDGET_PERCENT` | `75` | Percent of auto-detected RAM used for PostgreSQL recommendations. |
| `POSTGRES_TUNE_CPUS` | `auto` | CPU count used for worker recommendations. |
| `POSTGRES_TUNE_STORAGE` | `ssd` | One of `hdd`, `ssd`, `san`, or `nvme`. |
| `POSTGRES_TUNE_DB_SIZE` | `auto` | Use `less_ram` when the database comfortably fits in RAM, `mid_ram`, or `greater_ram` for very large databases. |
| `POSTGRES_TUNE_CONNECTIONS` | `100` | PostgreSQL `max_connections`; automatically raised if Silo's app pool is configured higher. |
| `POSTGRES_SHM_SIZE` | `8gb` | Docker `/dev/shm` size for the bundled PostgreSQL container. |

Advanced operators can still supply their own PostgreSQL configuration or
override these env vars. Set `POSTGRES_TUNE=off` when you do not want Silo to
change PostgreSQL server settings. Settings already written with `ALTER SYSTEM`
remain in `postgresql.auto.conf`; reset those PostgreSQL parameters if you later
move fully to a custom `postgresql.conf`.

## Build from Source

If you prefer running Bloem without Docker:

1. **Install prerequisites**: Go 1.26.4+, Node.js 22+, pnpm 10.32.1, PostgreSQL 18 with pgvector, Redis, and FFmpeg.

2. **Configure the source process**

   ```sh
   cp .env.example .env
   printf '\nSECRET_KEY=%s\nDATABASE_URL=%s\nREDIS_URL=%s\n' \
     "$(openssl rand -base64 48)" \
     'postgres://silo:silo@localhost:5432/silo?sslmode=disable' \
     'redis://localhost:6379' >> .env
   ```

   Change the URLs when you use existing services instead of the bundled development defaults.

3. **Start PostgreSQL and Redis** (skip if you already have them running)

   ```sh
   docker compose up -d postgres redis
   ```

4. **Build and run**

   ```sh
   make build
   ./silo
   ```

   The server starts at `http://localhost:8080` by default. All other settings are configured through the admin UI.

## Documentation

- [Admin guide](docs/wiki/admin-guide.md) — for the person running the server: install, first run, libraries, users and profiles, playback and transcoding, access policy, Live TV, maintenance and troubleshooting.
- [User guide](docs/wiki/user-guide.md) — for viewers: signing in, profiles, finding and playing things, downloads, requests, notifications, and using Jellyfin/Emby/Audiobookshelf apps with a Bloem server.
- [Wiki index](docs/wiki/index.md) — every operator- and viewer-facing page, including [Deploy Bloem with Docker](docs/wiki/deployment/docker.md), [Entitlement Templates](docs/wiki/admin/entitlement-templates.md), [Supported Media Folder Structures and Naming](docs/wiki/admin/media-folder-and-naming.md), [Collection Templates](docs/wiki/admin/collection-templates.md), [Local NFO Metadata](docs/wiki/admin/nfo-local-metadata.md) and [Monitoring Stream Nodes](docs/wiki/admin/monitoring-nodes.md).
- [DEVELOPMENT.md](DEVELOPMENT.md) — building from source, tests, migrations and project layout; [CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations.
- [FORK.md](FORK.md) — provenance, deliberate divergence from upstream and the protected-remote setup; [TRADEMARK.md](TRADEMARK.md) and [LICENSE](LICENSE).
- `docs/architecture/` and `docs/operations/` — design notes and operator runbooks, including the [v1 compatibility policy](docs/architecture/v1-scope.md), [entitlement-template operations](docs/operations/entitlement-templates.md), [bulk policy cohorts](docs/operations/bulk-policy-cohorts.md), [compatibility applications](docs/operations/compatibility-applications.md) and the [Canonical Settings API guide](docs/settings-api.md).
- [Apple Push Display Token](docs/notifications-push-api.md) — notification enrichment contract.

## Reporting Issues

Client implementers can use the [Canonical Settings API guide](docs/settings-api.md)
for contract discovery, contextual headers, remote scopes, effective reads, and
the admin projection.

If you are reporting a bug, install problem, or performance issue, start with the admin workflow and reproduction steps, not Claude/Codex analysis.

Please include:

- What you were trying to do
- Exact steps you took
- What you expected to happen
- What actually happened
- What exact action is slow or broken (`save`, `scan`, `browse`, `import`, `playback`, etc.)
- Whether it happens every time or only sometimes
- The library, media type, filter, setting, or value involved
- Version, branch, commit, and deployment details if you know them
- Screenshots, recordings, or log snippets if relevant

If you used Claude/Codex for debugging, put that under `Technical notes` at the end. Suspected files, SQL output, stack traces, and root-cause theories can be helpful, but only after the workflow and repro steps are clear.

Use this template:

```text
Goal:
Steps:
Expected:
Actual:
What is slow/broken:
Scope:
Version/branch:
Deployment:
Technical notes:
```

## Contributing & Development

Bloem Server is open source under the same terms as Silo. See
[DEVELOPMENT.md](DEVELOPMENT.md) for building from source in a dev workflow,
running tests, database migrations, and project layout, and
[CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations and merge
request guidance.

Since this is a tracking fork (see FORK.md), a change that belongs in Silo's
own scanner, catalog, playback, or plugin runtime is generally better
contributed upstream to [Silo Server](https://github.com/Silo-Server/silo-server)
directly, where it benefits every downstream project, not just this one.
Contributions to Bloem-specific areas — the `/api/bloem/v1` tenant foundation, the
plugin SDK/catalog, or the client-contract test suites — belong here.

## License & Trademarks

Silo's source code is licensed under the **GNU Affero General Public License
v3.0 or later** (`AGPL-3.0-or-later`) — see [LICENSE](LICENSE).

The **Silo name, logo, and wordmark are trademarks of Silo Media L.L.C.** and
are **not** covered by the AGPL. You're free to fork and redistribute the code,
but forks and redistributions must not use the Silo brand as their identity and
must remove or replace the brand assets. Publishing a Silo-branded app to an app
store requires written permission. See [TRADEMARK.md](TRADEMARK.md) for what's
permitted — including referential use like "compatible with Silo."
