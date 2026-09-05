---
title: Bloem Server Admin Guide
description: Everything a person running a Bloem Server needs, from a blank machine to a household streaming their own library, written for someone doing it for the first time.
summary: Install, first-run setup, libraries, users and profiles, playback and transcoding, access policy, notifications, compatibility with other apps, maintenance, and what to do when something breaks.
tags:
  - admin
  - operator
  - getting-started
  - deployment
audience:
  - operator
last_reviewed: 2026-09-05
related:
  - deployment/docker.md
  - admin/media-folder-and-naming.md
  - admin/entitlement-templates.md
  - admin/monitoring-nodes.md
  - user-guide.md
---

# Bloem Server Admin Guide

This is the handbook for the person who runs a Bloem Server: installs it, points it at a folder of
media, invites the household, and keeps it healthy. It assumes you have never done this before. If a
sentence uses a word you do not know, the [glossary](#glossary) at the end defines it.

If you are a *viewer* rather than the person running the server — you were given a login and want to
know how to watch things — you want the [User Guide](user-guide.md) instead.

**How this guide is organised.** Part 1 gets a server running. Part 2 is the day-to-day reference,
one section per area of the admin interface. Part 3 is troubleshooting. You can read Part 1 top to
bottom in an afternoon; Part 2 is for looking things up.

---

## Part 1 — Getting a server running

### 1.1 What Bloem Server is

Bloem Server is a media server: software that takes a folder of video, music, audiobook and ebook
files you already own, identifies what each file is, fetches artwork and descriptions for it, and
streams it to phones, tablets, televisions and web browsers on demand. It runs on a computer you
control — a home server, a spare PC, a rented virtual machine — and the people you invite connect to
it with the Bloem apps or with a web browser.

Three things it does that matter to how you set it up:

- **It plays media the way each device wants it.** If the device can play the file as-is, Bloem
  sends it unchanged ("direct play"). If not, Bloem converts it on the fly ("transcoding"). Transcoding
  is the one thing that needs real processing power, and it is why the GPU section below exists.
- **It has households.** One login can have several *profiles* — one per family member — each with
  its own watch history, its own recommendations and its own parental limits.
- **It talks other apps' languages.** Apps built for Jellyfin, Emby or Audiobookshelf can connect to a
  Bloem Server without knowing it is Bloem.

Bloem Server is a public tracking fork of Silo Server. That matters to you in one way: a handful of
technical names — environment variables like `SILO_DATA_ROOT`, the Docker service called `silo` — keep
their upstream spelling so that upstream improvements keep flowing in. Where this guide shows one of
those names, it is not a mistake.

### 1.2 What you need before you start

- **A machine to run it on.** Linux is the well-trodden path. Any 64-bit machine works for a small
  household; hardware transcoding (see 1.6) needs an Intel, AMD or NVIDIA GPU the machine can see.
- **Docker.** Docker Engine (Linux) or Docker Desktop (Mac/Windows), with Docker Compose 2.24 or
  newer. This guide installs Bloem with Docker because it is the path that is tested on every change;
  building from source is possible but is not covered here.
- **Your media, organised.** A folder tree Bloem can read, laid out the way
  [Supported Media Folder Structures and Naming](admin/media-folder-and-naming.md) describes. The
  single most common cause of "my films are not showing up" is a folder layout the scanner cannot make
  sense of, so it is worth reading that page before you scan.
- **Somewhere to keep the server's own data** — its database and the artwork it downloads. This is
  separate from your media and it must be on storage that persists when containers restart.
- **Git and OpenSSL** on the machine, for the quick-start commands.

You do *not* need a domain name or a public IP address to start. Everything below works on a home
network first; making it reachable from outside is a later, optional step.

### 1.3 Install

Copy these commands one block at a time. Each block is explained underneath it.

```sh
git clone https://github.com/bloem-studios/bloem-server.git
cd bloem-server
cp .env.example .env
chmod 600 .env
```

This downloads the deployment files and creates your settings file, `.env`. The `chmod` makes the
file readable only by you, because it is about to contain secrets.

```sh
printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
```

This generates two random secrets and appends them to `.env`: a database password, and
`SECRET_KEY`, the master key that encrypts every credential the server stores (metadata provider
keys, storage credentials, and so on).

> **Back up `SECRET_KEY` now, somewhere that is not the server.** If you lose it, every stored
> credential becomes unrecoverable, and a database backup will not save you — the backup contains
> the encrypted values, not the key. A password manager entry is fine.

Now open `.env` in a text editor and set the path to your media:

```dotenv
MEDIA_ROOT=/path/to/your/media
```

Use an absolute path (one that starts with `/`). This folder is mounted *into* the container, so
Bloem sees your files without copying them.

```sh
docker compose up -d
```

This downloads the Bloem image and its database and cache, and starts them. The first run takes a
few minutes. When it settles, open **http://localhost:8090** in a browser — or, from another
machine on the same network, `http://<the server's IP address>:8090`.

### 1.4 First run: the setup wizard

The first time you open the address, Bloem walks you through setup. You will be asked to:

1. **Create the first account.** This is the platform administrator — the account that can do
   everything. Pick a strong password; this login can change every other login.
2. **Confirm the server's address.** Bloem needs to know the URL people will use to reach it, so that
   links in emails and notifications point at the right place. On a home network this is the IP
   address you just used. If you later put it behind a domain name, come back and change it in
   **Admin → Settings → General**.
3. **Add your first library.** A library is one kind of media in one folder: "Films" pointing at
   `/media/films`, "Series" pointing at `/media/series`. The wizard asks for a name, a type (movies,
   series, music, audiobooks, books, comics) and the folder. Because `MEDIA_ROOT` is mounted into the
   container, the folder you pick here is the path *inside* the container — the wizard shows you the
   mounted tree so you can browse to it.

After the wizard, Bloem starts scanning. Scanning reads your folders, identifies each item and fetches
its details and artwork from the metadata providers. A first scan of a large library takes a while;
you can watch its progress in **Admin → Tasks**.

### 1.5 Where things live on disk

Two locations matter, and confusing them is the most common data-loss mistake:

| What | Where | Can I lose it? |
|---|---|---|
| **Your media** | `MEDIA_ROOT` on the host, mounted read-only into the container | No — Bloem never writes to it. |
| **Bloem's own data** — database, downloaded artwork, transcode cache, plugins | The Docker volumes and bind mounts the Compose file creates (`SILO_DATA_ROOT` and the PostgreSQL volume) | Yes, if you delete the volumes. This is what you back up. |

The full layout — which directory is which, what can be put on fast storage, what can be thrown
away — is in [Deploy Bloem with Docker → Storage and state](deployment/docker.md#storage-and-state).

### 1.6 Hardware transcoding (optional, recommended)

Without a GPU, transcoding uses the CPU. A modern CPU can manage one or two simultaneous streams
that need converting; beyond that, playback stutters. A GPU handles many at once.

- **Intel or AMD (VA-API / Quick Sync):** start with `docker compose -f docker-compose.yml -f
  docker-compose.vaapi.yml up -d` instead of the plain command. The host needs the GPU driver
  installed and `/dev/dri` present.
- **NVIDIA (NVENC):** install the NVIDIA container toolkit on the host, then use
  `docker-compose.nvidia.yml` the same way.

Then in **Admin → Settings → Playback**, set hardware acceleration to *auto*. Bloem probes what the
container can actually reach and falls back to software if the probe fails — a warning in the logs
that says NVENC could not load `libcuda` means the host driver is not visible to the container, not
that Bloem is broken. Details for each vendor: [Hardware acceleration](deployment/docker.md#hardware-acceleration).

### 1.7 Reaching it from outside your network (optional)

Bloem listens on port 8090 without encryption. That is fine inside a home network and not fine on the
internet. If you want to watch away from home, put a reverse proxy with HTTPS in front of it (Caddy,
nginx, Traefik) and point a domain name at that. Then set the server's address in **Admin → Settings →
General** to the `https://` domain so that links and apps use it. Bloem does not need any special
configuration to sit behind a proxy.

### 1.8 Invite the household

Every viewer needs an account, and every account can have profiles.

- **Admin → Users → Invite** sends an invitation to an email address. The invitee picks a password;
  their email address becomes their username. No account exists until they accept, so a mistyped
  address cannot squat a name. Invitations expire, and you can revoke one before it is used.
- **Invite codes** (Admin → Settings → Security & access → Invite codes) are for the case where you do
  not want to type each address: a code can be used a set number of times and hands out accounts with
  a policy you choose.
- **Profiles** are created by the account holder inside the app, or by you on their user page. A
  profile can have a PIN so that a child cannot switch to a parent's profile.

You are now running a media server. Everything from here on is the reference.

---

## Part 2 — The admin interface, area by area

Everything below is reached from the **Admin** entry in the web app's menu, which only platform
administrators see. Pages are named as they appear in the interface.

### 2.1 Dashboard

The landing page: who is watching right now, what the server is doing, recent activity, and the state
of the last scan. If something is wrong, it usually shows here first.

### 2.2 Libraries

One row per library. From here you:

- **Add a library** — name, type, folder(s). A library can span several folders.
- **Scan** — a full rescan of a library. You rarely need this by hand; see Autoscan below.
- **Refresh metadata** — re-fetch details and artwork without rescanning files. Use it after
  installing a new metadata plugin or fixing a badly matched item.
- **Set who can see it.** Libraries are per-user by default; access groups (2.5) and entitlement
  templates (2.6) decide who sees what.

**How Bloem decides what a file is.** The scanner reads the folder and file names first — that is
why naming matters — then asks the metadata providers to confirm. If it guesses wrong, open the item,
choose *Identify*, and pick the right match; the fix sticks across rescans. Local `.nfo` sidecar
files are honoured too: [Local NFO Metadata](admin/nfo-local-metadata.md) explains which fields, and
how they merge with what the providers say.

### 2.3 Autoscan

Watches your media folders and scans only what changed, so a newly added film appears within
minutes without a full rescan. Turn it on per library. If your media lives on a network share, folder
watching may not fire; set a scan interval instead.

### 2.4 Users and Devices

**Users** lists every account: its role (administrator or viewer), its profiles, its access groups,
and what it is entitled to. From a user's page you can reset a password, disable the account, change
its libraries, and see every device signed in as them.

**Devices** is the same information the other way round: every phone, TV and browser that has
signed in, with the option to sign one out remotely. Use this when a device is lost or sold.

### 2.5 Access groups

An access group is a named set of permissions — *can download*, *can request media*, *can use
Live TV* — that you attach to users. Make one group per kind of person ("Family", "Guests") rather
than setting permissions per user. Deleting a group moves its members to the default group; it never
leaves anyone without a policy.

### 2.6 Entitlement templates, organisations and policy

This is the part that scales past a household. If you run Bloem for a single family you can skip it;
the defaults are sensible.

An **entitlement template** is a reusable, versioned access policy: which libraries, whether
playback and transcoding are allowed, how many simultaneous streams, how many profiles, whether
downloads are permitted, the maximum quality, and which access-group permissions. Bloem ships
*Browse-only*, *Viewer*, *Standard*, *Premium* and *Reseller Member* as starting points. Every change
to a template creates a new **revision**; old revisions are never rewritten, so an account that was
given revision 3 keeps revision 3 until you deliberately move it.

An **organisation** is a group of accounts managed together — a reseller's customers, a second
family you host. Organisations have their own administrators, who can manage their own people but
not yours.

A **policy cohort** moves a reviewed set of existing accounts between exact template revisions, with
a preview of what will change first.

The safe workflow, in short: create or pick a template → create a new revision for each change →
on the organisation or account page choose the exact revision → **Preview** → read the diff →
**Confirm** within ten minutes. The full guide is [Entitlement Templates](admin/entitlement-templates.md).

### 2.7 Settings

**Admin → Settings** is organised into pages. The ones you will actually touch:

| Page | What is on it |
|---|---|
| **General** | Server name, the public address, language, time zone. |
| **Library & metadata** | Which metadata providers are used, in what order; artwork preferences; how aggressively to match. |
| **Providers** | API keys for TMDB, TVDB and the other providers, entered here and stored encrypted. Most providers need a free key before they return anything. |
| **Playback** | Hardware acceleration, transcode limits, the quality ladder, subtitle burn-in rules. |
| **Downloads** | Whether viewers can download for offline use, and limits. |
| **Security & access** | Password rules, invitations and invite codes, API keys, session lifetimes. |
| **Notifications** | What the server tells people about — new episodes, request updates — and through which channels (in-app, email, Discord, generic webhooks). |
| **Compatibility** | The Jellyfin/Emby and Audiobookshelf surfaces (2.12). |
| **Watch sync** | Syncing watch state to and from outside services. |
| **Infrastructure** | Search (Meilisearch), object storage for artwork and downloads (S3, MinIO, Cloudflare R2 — [S3 storage setup](../s3-storage-setup.md)), the distributed roles (2.14). |
| **AI** | Optional AI-assisted features such as description translation, each with its own provider key. |
| **Appearance** | The web app's look, and the theme viewers get by default. |

Every setting has a short explanation next to it in the interface. Changes are saved with the bar at
the bottom of the page; nothing applies until you save.

### 2.8 Collections and sections

**Collections** are curated groups of items — "Christmas films", "The Marvel one in order". You can
build them by hand, with rules (a smart collection: "everything rated above 8 from the 1990s"), or
from a **collection template** that syncs a list from TMDB, Trakt or MDBList and keeps it current:
[Collection Templates](admin/collection-templates.md).

**Sections** are the rows on the home screen. From here you decide what appears — Continue watching,
Recently added, a collection, a recommendation row — and in what order. Viewers can hide rows they do
not want; you decide what is on offer.

### 2.9 Requests

If you turn requests on (Access groups → *can request media*), viewers can ask for things you do not
have yet. **Admin → Requests** is the queue: approve, decline, mark as fulfilled. Fulfilled requests
notify the person who asked when the item appears in the library.

### 2.10 Live TV

Bloem can tune, record and show a programme guide for television from a network tuner. It ships as
a separate, attributed adaptation of Prairie Server's Live TV subsystem, which is why it feels like
its own corner of the product. **Admin → Live TV** has three parts:

- **Tuners.** Bloem talks to SiliconDust **HDHomeRun** tuners and to **Dispatcharr** (which makes
  IPTV sources look like an HDHomeRun). *Discover on LAN* finds tuners by broadcasting on your
  network; *Probe URL* asks one address directly (`http://192.168.1.50`, or a Dispatcharr base such
  as `http://dispatcharr.local:9191`). Candidates are checked before you can add them.
- **Guide (EPG).** Programme listings, from the tuner's own guide or from an XMLTV source you point
  it at.
- **Recording (DVR).** Rules for what to record and where recordings go.

> **Docker and LAN discovery.** The default Compose stack runs Bloem on a bridge network, which
> usually cannot send discovery broadcasts to your LAN, so *Discover on LAN* finds nothing. Either
> use *Probe URL* with the tuner's address (always works), or on Linux start with the Live TV
> override — `docker compose -f docker-compose.yml -f docker-compose.livetv.yml up -d` — which puts
> Bloem on the host network. It combines with the GPU overrides. On Docker Desktop (Mac/Windows) host
> networking does not reach your real LAN; use *Probe URL*.

Live TV transcoding has its own tab under Playback, because a live stream cannot be prepared in
advance the way a file can. Never expose a tuner's address to the internet; it has no login of its
own. Full detail: [Live TV tuner discovery](../livetv-tuner-discovery.md).

### 2.11 Plugins

Metadata providers, subtitle sources and other extensions are plugins. **Admin → Plugins** lists what
is installed, lets you enable and disable each one, and holds each plugin's own settings. Plugins
are installed by the person with access to the host, not from inside the web app — that is a
deliberate security boundary.

### 2.12 Compatibility with other apps (Jellyfin, Emby, Audiobookshelf)

Bloem answers the Jellyfin/Emby API and the Audiobookshelf API in the same process, on the same
address, with nothing extra to run. That means apps built for those servers — Infuse, VidHub,
Findroid, Audiobookshelf's own apps — can connect to your Bloem Server by entering its address as if
it were one of them.

- Turn each surface on or off in **Admin → Settings → Compatibility**. Off simply makes those
  paths answer "unavailable"; nothing is stopped or removed.
- If your reverse-proxy setup expects those servers on their usual ports, you can also give them a
  dedicated port: set `JF_PORT` and/or `ABS_PORT` in `.env` and uncomment the matching port lines in
  `docker-compose.yml`. The same-address path keeps working regardless.

Full details and troubleshooting: [Jellyfin/Emby and Audiobookshelf Compatibility](../operations/compatibility-applications.md).

### 2.13 Tasks, Logs, Diagnostics, Maintenance, Stats

- **Tasks** — every background job (scans, metadata refreshes, cleanups) with progress and history.
  If a scan seems stuck, look here first.
- **Logs** — the server log, filterable. When you ask for help, this is what to attach.
- **Diagnostics** — a one-click bundle of the information support needs, with credentials scrubbed.
- **Maintenance** — cache clearing, orphan cleanup, database housekeeping. Safe to run; nothing here
  deletes media.
- **Stats** and **Playback history** — what has been watched, by whom, how it was played (direct
  or transcoded). The transcode column is the quickest way to see whether your devices are playing
  files natively.

### 2.14 Nodes and distributed deployments

A single machine running everything is the *integrated* mode and is what you have after Part 1.
Bloem can also split into roles:

| Mode | What it does |
|---|---|
| `integrated` | Everything on one machine (the default). |
| `api` | The API and web app only, no local transcoding. |
| `transcode` | A worker that only transcodes and prepares downloads, sharing the same database. |
| `proxy` | A stream proxy in front of the others. |

You would do this to put transcoding on the machine with the GPU while the database lives somewhere
quieter. **Admin → Nodes** shows each worker, its GPU, its scratch disk and its load; [Monitoring
Stream Nodes](admin/monitoring-nodes.md) explains every column. The Compose examples for each role are
in [Server roles and distributed deployments](deployment/docker.md#server-roles-and-distributed-deployments).

### 2.15 Notifications and webhooks

**Admin → Settings → Notifications** decides what events are announced (new film, new episode, a
request approved) and to whom. Channels are in-app, email (needs an SMTP server — see the email
architecture note if you are setting one up), **Discord** (a webhook URL), and **generic webhooks**
for anything else. Generic webhooks are signed with HMAC so the receiver can verify them, and a
receiver that keeps failing is automatically disabled rather than retried forever.

---

## Part 3 — Keeping it healthy

### 3.1 Backups

Back up three things, and test restoring them once:

1. **The PostgreSQL database.** The Compose file includes a `pg_dump` example; run it on a schedule
   from the host, not inside the container. This is your watch history, users, settings, everything.
2. **`SILO_DATA_ROOT`** — artwork and plugin state. Losing it is recoverable (Bloem re-downloads
   artwork) but slow.
3. **`SECRET_KEY` from `.env`** — separately from the database, as explained in 1.3.

Your media is not part of the backup; back that up however you already do.

The step-by-step is in [Backups and updates](deployment/docker.md#backups-and-updates).

### 3.2 Updating

```sh
cd bloem-server
git pull
docker compose pull
docker compose up -d
```

Bloem migrates its own database on start. Read the release notes before a major update; if a
migration is mentioned, take a database backup first. To pin a specific version instead of following
the latest, set `SILO_IMAGE` in `.env` to a tagged image as described in
[Container image selection](deployment/docker.md#container-image-selection).

### 3.3 When something is wrong

| Symptom | First thing to check |
|---|---|
| Films are missing after a scan | Folder and file naming — [the naming guide](admin/media-folder-and-naming.md). Then **Admin → Tasks** for a scan error. |
| An item has the wrong artwork or title | Open it, choose *Identify*, pick the right match. |
| Playback stutters or buffers | **Playback history** — is it transcoding? If so, does the machine have a GPU, and is hardware acceleration set to *auto*? |
| "Cannot load libcuda" in the logs | The NVIDIA driver is not visible to the container. Check the container toolkit; Bloem has fallen back to software. |
| A Jellyfin/Emby app cannot connect | **Settings → Compatibility** — is that surface enabled? |
| Nobody can log in | Check the server address in **Settings → General** matches what people are typing, including `http` vs `https`. |
| The web app is slow to search | Enable Meilisearch under **Settings → Infrastructure** — [Optional Meilisearch](deployment/docker.md#optional-meilisearch). |
| The database container will not start after an update | Look at `docker compose logs postgres`; auto-tuning may have written a setting that needs a restart — `docker compose restart postgres`. |

When you ask for help, attach the diagnostics bundle (**Admin → Diagnostics**) rather than
screenshots of the log; it contains what is needed and nothing secret.

---

## Glossary

- **Access group** — a named set of permissions attached to users.
- **Autoscan** — watching folders and scanning only what changed.
- **Direct play** — sending a file to a device unchanged, because the device can play it as-is.
- **Entitlement template** — a reusable, versioned access policy applied to accounts or organisations.
- **Household / profile** — one account, several people; each profile has its own history and limits.
- **Library** — one kind of media in one or more folders.
- **Metadata** — the title, description, artwork, cast and ratings Bloem fetches for each item.
- **Metadata provider** — an outside service (TMDB, TVDB…) that supplies metadata, usually via a plugin with an API key.
- **NFO** — a small text file next to a media file that carries metadata Bloem will honour.
- **Node** — a separate machine running one of Bloem's roles.
- **Organisation** — a group of accounts with its own administrators.
- **Revision** — one immutable version of an entitlement template.
- **Transcoding** — converting media on the fly so a device can play it.

## Source References

- `README.md` — highlights, configuration, server modes, PostgreSQL auto-tuning
- `docs/wiki/deployment/docker.md` — the deployment reference this guide summarises
- `docs/operations/compatibility-applications.md`
- `docs/architecture/invitations-onboarding.md`
- `docs/architecture/notifications.md`
- `docs/livetv-tuner-discovery.md`, `docs/s3-storage-setup.md`
- `web/src/pages/` — the admin and settings pages named above
