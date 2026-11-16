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

You need Docker with Compose 2.24 or newer, Git, OpenSSL and a folder of media.

```sh
git clone https://github.com/Bloem-Studios/bloem-server.git
cd bloem-server
cp .env.bloem.example .env
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

The image is public: `ghcr.io/bloem-studios/bloem-server`. `.env.bloem.example` uses `latest`, the image Bloem's own
production runs, which moves at most once a day. Pin a dated tag such as `2026.11.17` to choose when you update, or use
`edge` for every commit that passes CI. To build your own instead, run `docker build -t bloem-server:local .` and set
`SILO_IMAGE=bloem-server:local`.

`.env.bloem.example` selects Silo's multi-container Compose file (`docker-compose.advanced.yml`); keep that line, because
Silo's default `docker-compose.yml` starts Silo's own image instead. The
[Docker guide](../docs/bloem/overlays/wiki/deployment/docker.md) covers hardware transcoding, reverse proxies, search and
updates.

## Documentation

- [Admin guide](../docs/wiki/admin-guide.md) and [user guide](../docs/wiki/user-guide.md): running a server, and using one.
- [Bloem Server in depth](../docs/bloem/about.md): where Bloem comes from, everything it adds and removed, deployment and
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

## Licence, trademarks and credit

Bloem Server is licensed under the GNU Affero General Public License v3.0 or later, like Silo; see [LICENSE](../LICENSE).
Most of its code is Silo's, carried forward with Silo's licence and copyright notices intact. If you find it useful, the
credit, and any support you would like to give, belongs first to the people building
[Silo](https://github.com/sponsors/quick104).

The Silo name, logo and wordmark are trademarks of Silo Media L.L.C. and are not covered by the AGPL. Bloem Server is not
Silo and is not made or endorsed by Silo's developers; it carries Bloem's own name and artwork, as
[TRADEMARK.md](../TRADEMARK.md) requires of forks.
