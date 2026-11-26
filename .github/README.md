<p align="center">
  <img src="../assets/bloem-banner.png" alt="Bloem Server" width="520" />
</p>

# Bloem Server

> [!CAUTION]
> **Offered without support.** Bloem Server is provided as-is, with no support of any kind. Issues are turned off, questions go unanswered, and pull requests are not accepted: they are closed without review. You are free to fork it under the AGPL v3.

Bloem Server is a self-hosted media server for films, series, audiobooks, ebooks, podcasts and manga. It is a fork of
[Silo Server](https://github.com/Silo-Server/silo-server), maintained by Bloem Studios. Bloem merges Silo's `main` as it
moves instead of copying it once, so most of what you run is Silo: the scanner, the catalog, playback (direct play,
remux, hardware transcoding), the web app and the plugin runtime.

## Built on Silo

**Bloem Server would not exist without [Silo Server](https://github.com/Silo-Server/silo-server) and the people who
build it. Thank you.** Nearly everything you use when you run Bloem is their work:

- the scanner that turns folders into a library, and the catalog and metadata plugins behind it;
- playback: direct play, remux, hardware transcoding and transcode nodes;
- the web app, the admin console, and households with their own profiles and watch history;
- the plugin runtime and its SDK;
- Jellyfin and Audiobookshelf compatibility, so the apps people already use keep working.

Silo is one of the best things in self-hosted media right now. It is built on a modern foundation (PostgreSQL, Redis,
S3), it stays fast on large libraries, it is carefully designed, and it gets better every day. Bloem takes in Silo's
work twice a day, so a Bloem server gets Silo's fixes and features within hours of them landing. When we fix a bug in
Silo's own code, we send the fix to Silo as a pull request as well.

If Bloem is useful to you, the thanks belong to Silo first:

- **Run Silo.** If you don't need Bloem's organizations, Live TV or storage plugins, [Silo](https://siloserver.org) is
  the original, and it is excellent.
- **Sponsor Silo** on [GitHub Sponsors](https://github.com/sponsors/quick104).
- **Join Silo's community** on [Discord](https://discord.gg/siloserver) and read the
  [Silo manual](https://siloserver.org/docs): most of it applies to Bloem too.
- **Keep Bloem's problems away from Silo.** Silo's developers don't support Bloem. Report a bug to Silo only if it also
  happens on an unmodified Silo server.

## What Bloem adds

- **Organizations.** One server can host several organizations, each with its own people, profiles, libraries, plans,
  web addresses and branding, and each viewer sees only what they are entitled to.
- **Live TV and DVR.** Tuners, Xtream providers, a programme guide and recordings.
- **Storage plugins.** Libraries can read from storage plugins, such as S3, as well as from folders. Experimental.
- **Announcements and promotions.** Messages to viewers, promotions and seasonal presentation, written in the web admin.
- **Apps.** A native API (`/api/bloem/v2`) for Bloem's own apps, alongside Silo's `/api/v2`.

A Bloem server can take over an existing Silo installation on the same database and hand it back again. See
[switching between Silo and Bloem](../docs/architecture/bloem-silo-switching-explained.md).

## Run it

Bloem runs from its public image, `ghcr.io/bloem-studios/bloem-server`; there is nothing to build. You need Docker with
Compose 2.24 or newer, OpenSSL and a folder of media. Download the Compose file and the settings template into an
empty folder and generate the two secrets:

```sh
mkdir bloem && cd bloem
curl -fsSL -o docker-compose.advanced.yml https://raw.githubusercontent.com/Bloem-Studios/bloem-server/main/docker-compose.advanced.yml
curl -fsSL -o .env https://raw.githubusercontent.com/Bloem-Studios/bloem-server/main/.env.bloem.example
chmod 600 .env
printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
```

Open `.env`, point `MEDIA_ROOT` at the absolute path of your media, then start it:

```sh
docker compose up -d
```

Open <http://localhost:8090> and the setup wizard creates the administrator account and the first library. Keep a copy
of `SECRET_KEY` somewhere other than the server: it encrypts every stored credential, and a database backup does not
contain it.

**Which image.** `.env` uses `latest`, the image Bloem's own production runs, which moves after each verified deploy;
update with `docker compose pull && docker compose up -d`. Pin a dated tag such as `2026.11.26` in `SILO_IMAGE` to
choose when you update. `edge` no longer moves; if you use it, switch to `latest` or a dated tag. Keep
`COMPOSE_FILE=docker-compose.advanced.yml` in `.env`:
Silo's own `docker-compose.yml` starts Silo, not Bloem. The
[Docker guide](../docs/bloem/overlays/wiki/deployment/docker.md) covers hardware transcoding, reverse proxies, search,
updates and building the image yourself.

## Documentation

- [Admin guide](../docs/wiki/admin-guide.md) and [user guide](../docs/wiki/user-guide.md): running a server, and using one.
- [Bloem Server in depth](../docs/bloem/about.md): where Bloem comes from, everything it adds, deployment and
  configuration, and building from source.
- [Documentation index](../docs/bloem/README.md): every Bloem page, including how organizations, plans, Live TV, storage
  and apps work.
- [How Bloem keeps up with Silo](../docs/architecture/bloem-following-silo.md) and [FORK.md](../FORK.md): how Silo's
  changes reach Bloem.

The root `README.md` is Silo's own and stays exactly as Silo wrote it, so that Silo's changes keep merging cleanly.

## Security

Report a vulnerability privately through
[GitHub's private vulnerability reporting](https://github.com/Bloem-Studios/bloem-server/security/advisories/new). Do not
put it anywhere public.

## Licence and trademarks

Bloem Server is licensed under the GNU Affero General Public License v3.0 or later, like Silo; see [LICENSE](../LICENSE).
Most of its code is Silo's, carried forward with Silo's licence and copyright notices intact. Bloem's Live TV is an
attributed adaptation of [Prairie Server](https://github.com/Prairie-Server/prairie-server).

The Silo name, logo and wordmark are trademarks of Silo Media L.L.C. and are not covered by the AGPL. Bloem Server is not
Silo and is not made or endorsed by Silo's developers; it carries Bloem's own name and artwork, as
[TRADEMARK.md](../TRADEMARK.md) requires of forks.
