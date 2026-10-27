---
title: Bloem Server Admin Guide
description: Everything a person running a Bloem Server needs, from a blank machine to a household streaming their own library, written for someone doing it for the first time.
summary: Install, first-run setup, libraries, users and profiles, playback and transcoding, access policy, notifications, native clients, maintenance, and what to do when something breaks.
tags:
  - admin
  - operator
  - getting-started
  - deployment
audience:
  - operator
last_reviewed: 2026-10-27
related:
  - ../bloem/overlays/wiki/deployment/docker.md
  - ../architecture/media-naming.md
  - admin/entitlement-templates.md
  - ../architecture/observability.md
  - ../architecture/ai-provider-authentication.md
  - ../../FORK.md
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

> **Review status.** Checked on 2026-10-27 against Bloem `main` at `e852f92bf`, which contains
> Silo through `3e2d306b2`: the web routes, navigation and page labels in `web/src/`, the
> Compose files and the server code named in each section. This is a source review. It is not
> a record of a browser, device or playback test, and it does not describe any particular
> deployment. The guide was first written in `15f7ada52` (2026-09-05).

---

## Where Bloem comes from

Bloem Server is a public **tracking fork** of [Silo Server](https://github.com/Silo-Server/silo-server):
Bloem's repository follows Silo's `main` branch, merges it regularly, and keeps its own changes
small and declared, so that Silo's improvements keep arriving. [FORK.md](../../FORK.md) states the
rules and the [integration changelog](../bloem/CHANGELOG.md) records each merge.

**Almost everything in this guide is Silo's.** The web app, the setup wizard, libraries and
scanning, metadata, playback and transcoding, profiles and households, access groups, movie and
series requests, notifications, plugins, the settings pages, AI Services, recommendations,
autoscan, server nodes, and the Jellyfin- and Audiobookshelf-compatible servers all come from
Silo. Where Silo documents a feature, its own pages in this repository are the reference, for
example [real-time monitoring](../architecture/realtime-monitoring.md),
[requests](../architecture/media-requests.md), [notifications](../architecture/notifications.md)
and [AI providers](../architecture/ai-provider-authentication.md). Silo's user manual for people
running a server is a separate repository,
[`Silo-Server/siloserver.org`](https://github.com/Silo-Server/siloserver.org).
Because the web app is Silo's, parts of the interface still say "Silo": the setup wizard ends
on "Silo is ready", and settings are labelled **Silo public URL** or **Silo passwords**. That
text is Silo's, not an error.

**What Bloem adds** on top of Silo, each covered below:

- **Organisations and administrative contexts**: several groups of accounts managed separately
  on one server, each with its own administrators ([2.6](#26-entitlement-templates-organisations-and-policy)).
- **Entitlement templates and policy cohorts**: reusable, versioned access policy
  ([Entitlement Templates](admin/entitlement-templates.md)).
- **Live TV**: tuners, a programme guide and recording, adapted from Prairie Server, plus
  Xtream providers ([2.10](#210-live-tv)).
- **Campaigns, seasonal packs and announcements** ([2.16](#216-campaigns-and-seasonal-packs)).
- **Browse-only previews** for prospective subscribers ([2.6](#browse-only-previews)).
- **Audiobook and ebook requests** beside Silo's movie and series requests ([2.9](#29-requests)).
- **Storage sources**: libraries that read from S3 or Bookwarehouse buckets as well as local
  folders ([2.2](#22-libraries)).
- **Profile sign-in credentials** for supported native clients ([user guide](user-guide.md#2-profiles)).
- A native API for Bloem's own clients, `/api/bloem/v1` ([reference](../bloem-api-reference.md)).

**What Bloem removed.** Some earlier Bloem features are gone, and this guide no longer
describes them: the music catalog (`f9e1447f3`, 2026-10-26), Pastime managed tracking
(2026-10-13, see [Pastime removal](../architecture/bloem-pastime-removal.md)) and Bloem's companion
enrollment for compatibility apps (`b8ddec9bf`, 2026-10-13; Silo's own Jellyfin and
Audiobookshelf servers came back on 2026-10-18 in `2393c41ee`). Silo, for its part, removed the
administrator's colour, font and custom-CSS controls (`3173b22a1`, #2413, 2026-10-13) and made
Cinema Dark the only theme (`39a554717`, #1534).

### What changed most recently

Bloem merged Silo through `3e2d306b2` on 2026-10-27 (`68070f04b`). For operators and viewers:

- **AI providers are connected once and chosen per job** (Silo `c27acf246`, #3106). See
  [2.17](#217-ai-services-and-recommendation-embeddings).
- **The recommendation embedding provider is picked from those providers** (Silo `39913c51d`,
  #3107), on the Recommendations page.
- **Playback can start on a different audio track** when the preferred one would need
  converting for that device (Silo `e7605afe2`, #3115). See [2.13](#213-playback-history-logs-diagnostics-tasks-and-maintenance).
- **Playback progress and stop are bound to the profile.** One profile on an account can no
  longer report progress for, or stop, another profile's playback (Silo `e852571bb`, #3133;
  Bloem's own durable stop follows the same rule).
- Security hardening: sign-in tokens are bound to their session's account, signed stream and
  download links are masked in logs, and account-wide downloads check the requesting
  profile's access. Nothing needs configuring.

---

## Part 1 — Getting a server running

### 1.1 What Bloem Server is

Bloem Server is a media server: software that takes a folder of video, audiobook, ebook and comic
files you already own, identifies what each file is, fetches artwork and descriptions for it, and
streams it to phones, tablets, televisions and web browsers on demand. It runs on a computer you
control — a home server, a spare PC, a rented virtual machine — and the people you invite connect to
it with a web browser, with an app that speaks Bloem's or Silo's API, or with a Jellyfin- or
Audiobookshelf-compatible app.

Three things it does that matter to how you set it up:

- **It plays media the way each device wants it.** If the device can play the file as-is, Bloem
  sends it unchanged ("direct play"). If not, Bloem converts it on the fly ("transcoding"). Transcoding
  is the one thing that needs real processing power, and it is why the GPU section below exists.
- **It has households.** One login can have several *profiles* — one per family member — each with
  its own watch history, its own recommendations and its own parental limits.
- **It serves native clients.** Use the embedded web app or a native client whose API contract
  matches the server.

Because Bloem tracks Silo, a handful of technical names keep their upstream spelling so that
upstream improvements keep flowing in: environment variables like `SILO_DATA_ROOT` and
`SILO_IMAGE`, the Docker service called `silo`, the default database user `silo`. Where this guide
shows one of those names, it is not a mistake.

### 1.2 What you need before you start

- **A machine to run it on.** Linux is the well-trodden path. Any 64-bit machine works for a small
  household; hardware transcoding (see 1.6) needs an Intel, AMD or NVIDIA GPU the machine can see.
- **Docker.** Docker Engine (Linux) or Docker Desktop (Mac/Windows), with Docker Compose 2.24 or
  newer. This guide installs Bloem with Docker; building from source is possible but is not
  covered here.
- **Your media, organised.** A folder tree Bloem can read, laid out the way
  [Supported Media Folder Structures and Naming](../architecture/media-naming.md) (Silo's page)
  describes. The single most common cause of "my films are not showing up" is a folder layout the
  scanner cannot make sense of, so it is worth reading that page before you scan.
- **Somewhere to keep the server's own data** — its database and the artwork it downloads. This is
  separate from your media and it must be on storage that persists when containers restart.
- **Git and OpenSSL** on the machine, for the quick-start commands.
- **Access to a Bloem image.** See the note in 1.3 before you start.

You do *not* need a domain name or a public IP address to start. Everything below works on a home
network first; making it reachable from outside is a later, optional step.

### 1.3 Install

Copy these commands one block at a time. Each block is explained underneath it.

```sh
git clone https://github.com/bloem-studios/bloem-server.git
cd bloem-server
cp .env.bloem.example .env
chmod 600 .env
```

This downloads the deployment files and creates your settings file, `.env`, from Bloem's example.
Use `.env.bloem.example`, not `.env.example`: `.env.example` is Silo's file, unchanged, and its
`SILO_IMAGE` line selects Silo's image instead of Bloem's. The `chmod` makes the file readable only
by you, because it is about to contain secrets.

```sh
printf '\nPOSTGRES_PASSWORD=%s\nSECRET_KEY=%s\n' \
  "$(openssl rand -hex 24)" "$(openssl rand -base64 48)" >> .env
```

This generates two random secrets and appends them to `.env`: a database password, and
`SECRET_KEY`, the master key that encrypts every credential the server stores (metadata provider
keys, storage credentials, and so on). The server refuses to start without it.

> **Back up `SECRET_KEY` now, somewhere that is not the server.** If you lose it, every stored
> credential becomes unrecoverable, and a database backup will not save you — the backup contains
> the encrypted values, not the key. A password manager entry is fine.

Now open `.env` in a text editor and set the path to your media:

```dotenv
MEDIA_ROOT=/path/to/your/media
```

Use an absolute path (one that starts with `/`). This folder is mounted read-only *into* the
container at `/mnt/media`, so Bloem sees your files without copying them.

**Choose the image.** `.env.bloem.example` sets `SILO_IMAGE=ghcr.io/bloem-studios/bloem-server:latest`.
Bloem's CI publishes each tested `main` commit under its full commit SHA and, since October 27, 2026,
moves `latest` to the newest one, so `latest` changes whenever `main` does. To know which revision you
run and roll back to it, set `SILO_IMAGE` to a full-SHA tag instead, or build the image yourself, as described in
[Container image selection](../bloem/overlays/wiki/deployment/docker.md#container-image-selection).
That page also notes that the image package may need a GitHub account with access to pull it.

```sh
docker compose up -d
```

This downloads the Bloem image, PostgreSQL and Redis, and starts them. The first run takes a
few minutes. When it settles, open **http://localhost:8090** in a browser — or, from another
machine on the same network, `http://<the server's IP address>:8090`.

### 1.4 First run: the setup wizard

The first time you open the address, the server walks you through Silo's setup wizard. Only the
first step is required; every later step can be skipped and revisited from the step list until
you leave the wizard on its last screen. The steps are:

1. **Account.** Create the first account — the administrator who can do everything — and its first
   profile. Pick a strong password; this login can change every other login.
2. **Server.** Name the server (shown on the sign-in page and in the apps) and set the **Public URL**,
   the address people open in a browser, which is also used for links in emails and notifications.
   On a home network this is the address you just used. If you put the server behind a reverse
   proxy, list the proxy under **Trusted proxies**.
3. **Playback.** Video transcoding on or off, hardware acceleration (*Auto* picks the best device the
   server can see), 4K transcoding and HDR tone mapping.
4. **Storage.** Optional S3 storage for artwork and uploads; leave it unset to keep them on local disk.
5. **Libraries.** Add your media. A library is one kind of media in one or more folders. Its type is
   one of Movies, Series, Mixed, Audiobooks, Ebooks, Manga or Podcasts. Because `MEDIA_ROOT` is
   mounted into the container, the folder you pick is the path *inside* the container (under
   `/mnt/media`); the folder browser shows you the mounted tree.
6. **Subtitles.** Accounts for subtitle sources such as OpenSubtitles.
7. **Connect apps.** Whether to answer Jellyfin-compatible apps, and the address they should use.
8. **Features.** Skip markers (TheIntroDB and Silo's marker service, or detection on this server),
   mobile push notifications, and whether people may download.

After the wizard, the server starts scanning. Scanning reads your folders, identifies each item and
fetches its details and artwork from the metadata providers. A first scan of a large library takes a
while; you can watch it on **Libraries** and in **Scheduled Tasks**.

### 1.5 Where things live on disk

Two locations matter, and confusing them is the most common data-loss mistake:

| What | Where | Can I lose it? |
|---|---|---|
| **Your media** | `MEDIA_ROOT` on the host, mounted read-only into the container | No — Bloem never writes to it. |
| **Bloem's own data** — database, Redis, downloaded artwork, plugins, compatibility assets, prepared downloads, transcode scratch | Bind mounts under `SILO_DATA_ROOT` (default `/opt/silo`), including `/opt/silo/postgres` for the database | Yes, if you delete them. The database is what you back up. |

The full layout — which directory is which, what can be put on fast storage, what can be thrown
away — is in [Deploy Bloem with Docker → Storage and state](../bloem/overlays/wiki/deployment/docker.md#storage-and-state).

### 1.6 Hardware transcoding (optional, recommended)

Without a GPU, transcoding uses the CPU. A modern CPU can manage one or two simultaneous streams
that need converting; beyond that, playback stutters. A GPU handles many at once.

- **Intel or AMD (VA-API / Quick Sync):** start with `docker compose -f docker-compose.yml -f
  docker-compose.vaapi.yml up -d` instead of the plain command. The host needs the GPU driver
  installed and `/dev/dri` present.
- **NVIDIA (NVENC):** install the NVIDIA driver and the NVIDIA Container Toolkit on the host, then
  use `docker-compose.nvidia.yml` the same way.

Then open **Playback → Settings** in the admin sidebar and set **Hardware acceleration** to *Auto*
(the other choices are Intel Quick Sync, VA-API, NVIDIA NVENC, VideoToolbox and Software).
Details for each vendor: [Hardware acceleration](../bloem/overlays/wiki/deployment/docker.md#hardware-acceleration).

### 1.7 Reaching it from outside your network (optional)

Bloem listens on port 8090 without encryption. That is fine inside a home network and not fine on the
internet. If you want to watch away from home, put a reverse proxy with HTTPS in front of it (Caddy,
nginx, Traefik) and point a domain name at that. Then, under **Server → General**, set **Silo public
URL** to the `https://` address so that links and apps use it, and add the proxy's address to
**Trusted proxies** so the server sees each viewer's real address. Do not expose PostgreSQL or Redis.

### 1.8 Invite the household

Every viewer needs an account, and every account can have profiles. Open **Users** in the admin
sidebar; the page has three tabs.

- **Invitations** sends an invitation to an email address, or creates a link you deliver yourself
  (email needs a mail server, see **Integrations → Email**). The invitee picks a password. No account
  exists until they accept, so a mistyped address cannot squat a name. You can replace or revoke a
  link before it is used.
- **Invite Codes** are for the case where you do not want to type each address: a code can be used a
  set number of times. People enter it on the sign-up page. Public sign-ups and self-service password
  reset are switched on or off under **Sign-in & Security → Sign-in**.
- **Users** lists the accounts; **Create User** makes one directly with a username and password.
- **Profiles** are created by the account holder (on the profile picker or under **Settings →
  Profiles**) or by you on their user page. A profile can have a PIN so that a child cannot switch to
  a parent's profile.

For an organisation, select its administrative context and open **Invitations**.
That flow creates user invitations and shows a link once for you to deliver; it sends
no email. Regenerating a link invalidates the previous one, and revocation prevents
acceptance. Review the confirmation before either action. Copy the new link before
closing the result: it is cleared when you close it, switch context or sign out.
After a connection error, reload the list before making a new decision.

You are now running a media server. Everything from here on is the reference.

---

## Part 2 — The admin interface, area by area

Everything below is reached from the **Admin** entry in the sidebar when your account has
administrative access. Bloem first asks for an **administrative context** (the control in the
navigation, **Choose context**):

- **Platform** is the whole server. Its sidebar has Silo's three groups, **Monitor**, **Manage** and
  **Configure**, plus Bloem's **Platform** group (Organizations, Entitlement Templates, Campaigns &
  Seasonal Packs, Direct Accounts, Bulk Account Policies) and **Live TV** under Manage.
- **An organisation** shows only that organisation's pages: Overview, People, Policy Cohorts, Access
  Groups, Libraries & Entitlements, Storage sources, Invitations, Policy Decisions and Activity &
  Audit.

The pages you see follow your authority. Being the primary profile of a household does not make
the account a platform administrator, and an organisation administrator cannot open platform pages.
Admin pages also need a selected profile; the account's settings and sign-in sessions do not.

### 2.1 Dashboard

**Monitor → Dashboard** is the landing page: live sessions, content health and server activity.
**Monitor → Activity** shows each current stream: who, what, which client and whether it is
playing directly or being converted. If something is wrong, it usually shows here first.

### 2.2 Libraries

**Manage → Libraries** has one row per library. From here you:

- **Add a library** — name, type, folder(s). A library can span several folders.
- **Scan Library** — a full rescan of a library. You rarely need this by hand; see Autoscan below.
  **Verify Mounts** and **Check mount** test that the folders are reachable.
- **Refresh Metadata** — re-fetch details and artwork without rescanning files. Use it after
  installing a new metadata plugin or fixing a badly matched item.
- **Unmatched and ignored items** — the files the scanner could not match to any provider, which you
  can resolve or ignore.
- **Set who can see it.** Access groups (2.5) and entitlement templates (2.6) decide who sees what.

**How Bloem decides what a file is.** The scanner reads the folder and file names first — that is
why naming matters — then asks the metadata providers to confirm. If it guesses wrong, open the item,
choose **Match Item** from its menu, and pick the right match; **Edit Metadata** changes individual
fields. Local `.nfo` sidecar files are honoured too: [Local NFO Metadata](../architecture/local-nfo-metadata.md)
(Silo's page) explains which fields, and how they merge with what the providers say.

**Libraries with local and remote folders** (Bloem). A library can combine filesystem
paths with several storage-plugin locations. In the folder picker, open a configured
S3 or Bookwarehouse source, browse into nested directories and add the required
roots. Review the whole selection before saving. Removing one selected root does
not discard the others, and reopening the picker preserves existing selections.
A library location keeps its source identity when its selected roots change.

Native storage supports ebook, movie and audiobook workflows only when the
corresponding scanner and delivery services are ready. Configure credentials
through the protected source controls (for an organisation, its **Storage sources** page); do
not paste provider URLs into media paths. Provider installation, source configuration, library
admission and actual playback are separate checks. An unavailable source can be retained
for recovery without publishing unverified media. See [native storage](../architecture/bloem-native-storage.md)
and its [API reference](../bloem-api-reference.md#native-storage-administration).

### 2.3 Autoscan

Silo's Linux real-time library monitoring watches local folders. It scans the changed file or
subtree and reports each folder's monitoring status. This is separate from external autoscan
connections. Network shares are not watched by inotify: use scheduled scans, a manual scan or an
external autoscan source. See [real-time monitoring](../architecture/realtime-monitoring.md) (Silo's page).

**Manage → Autoscan** has **Activity** (changes received and the scans they started), **Sources**
(folders, webhooks and servers Autoscan watches) and **Settings** (polling defaults and
connections). On Sources, **Run now** polls every polling source immediately, and each source opens
an edit dialog for changes. Dropped webhook deliveries do not update the last delivery time.
Overlapping scans wait for the active scan instead of silently skipping the requested work.

### 2.4 Users and Devices

**Manage → Users** has **Users**, **Access Groups**, **Devices** and **History Import**.

**Users** lists every account with its role and access group. A user's page has tabs for
Overview, Access, Sign-in, Activity, Downloads and Preferences. From there you can disable the
account, change its access and limits, and help with passwords: for local-password accounts you
can email or share a single-use reset link, or set a temporary password that must be replaced at
the next sign-in. The server owner is a distinct account role from organisation ownership; only
the server owner can change another administrator's account. See
[password resets](../architecture/password-resets.md) (Silo's page).

**Inherited profile limits.** An unset account `max_profiles` inherits the access
group's limit; it is not an unlimited allowance. An explicit account value takes
precedence for ordinary accounts. Managed Bloem entitlements keep their assigned
ceiling. Review the effective policy before changing a household's profile count.

**Devices** lists every device that has connected with a stable device ID, with its per-profile
setting overrides, which you can reset. To cut off a lost or sold device, the account holder signs it
out under **Settings → Signed-in sessions**; you can also disable the account or reset its password.

**History Import** copies watch history from Emby, Plex or Jellyfin into Bloem.

### 2.5 Access groups

An access group is a named set of permissions and defaults — libraries, downloads, streams,
requests and permissions such as *Watch Live TV*, *Marker Editing* or *Metadata Curation* — that you
attach to users. Make one group per kind of person ("Family", "Guests") rather than setting
permissions per user. Deleting a group moves its members to the default group in the same
organisation; it never leaves anyone without a policy. Group policy also includes local/remote
stream bitrate limits and request approval/quota settings, with account overrides and server
defaults. See [request policy](../architecture/media-requests.md). Access groups are Silo's; Bloem
scopes them to an organisation.

### 2.6 Entitlement templates, organisations and policy

This is the part that scales past a household, and it is Bloem's. If you run Bloem for a single
family you can skip it; the defaults are sensible.

An **entitlement template** is a reusable, versioned access policy: which libraries, whether
playback and transcoding are allowed, how many simultaneous streams, how many profiles, whether
downloads are permitted, the maximum quality, and which access-group permissions. Bloem ships
*Browse-only*, *Viewer*, *Standard*, *Premium* and *Reseller Member* as starting points, plus the
protected *Preview* template used by browse-only previews. Every change to a template creates a
new **revision**; old revisions are never rewritten, so an account that was given revision 3 keeps
revision 3 until you deliberately move it.

An **organisation** is a group of accounts managed together — a reseller's customers, a second
family you host. Each account belongs to exactly one organisation. Organisations have their own
administrators, who can manage their own people but not yours. Platform administrators manage
organisations under **Platform → Organizations** and apply templates to platform-managed
accounts under **Direct Accounts**.

In an organisation's **Libraries & Entitlements** page, distinguish owned libraries from grants.
You can suspend, restore or withdraw an existing grant after confirmation; withdrawal
cannot be undone from this page. If someone changed it since you loaded it, reload
and review the current grant before acting. **Activity & Audit** shows redacted lifecycle
and entitlement events for this organisation in pages of 50; load more explicitly.
It is a record of events these ledgers contain, not a guarantee that every action
appears. **Policy Decisions** is a separate diagnostic page.

A **policy cohort** moves a reviewed set of existing accounts between exact template revisions, with
a preview of what will change first (**Policy Cohorts** in an organisation, **Bulk Account Policies**
for direct accounts).

The safe workflow, in short: create or pick a template → create a new revision for each change →
on the organisation or account page choose the exact revision → **Preview** → read the diff →
**Confirm** within ten minutes. The full guide is [Entitlement Templates](admin/entitlement-templates.md).

#### Browse-only previews

The protected **Preview** entitlement lets a prospect browse selected libraries,
descriptions, series, seasons and episodes. Playback, reading, downloads and
personal changes are refused. Unlike an ordinary policy with playback disabled,
the protected preview policy also restricts credentials and catalogue responses.

For a public web preview, configure the companion Garden application's library
preview integration with a separate preview host. Garden is Bloem's companion application,
outside this repository. It issues short-lived launch links; the server exchanges each ticket
once for a bounded guest session. Keep the normal subscriber host separate. Server setup uses the
scoped preview administration API; see the [API reference](../bloem-api-reference.md#preview-guest-sessions)
and the [security contract](../architecture/bloem-browse-only-content-policy.md).
Source publication alone does not configure hosts or enable the preview.

### 2.7 Settings

Silo's settings pages are spread through the admin sidebar under the topic they belong to; there is
no single "Settings" menu. Each page is at `/admin/settings/<page>`. The ones you will actually touch:

| Sidebar location | Page | What is on it |
|---|---|---|
| Configure → Server | **General** | Server name and login subtitle, **Silo public URL**, **Show on the local network** (Silo's LAN advertisement), local network address, trusted proxies, database connections, Redis. |
| Configure → Server | **Appearance** | Logos and icons, favicon, login background, accent colour, server-wide poster-badge defaults and title-art default. There are no theme, font or custom-CSS controls. |
| Configure → Server | **Storage** | Where artwork and uploads are stored: local disk or S3 ([S3 storage setup](../bloem/overlays/s3-storage-setup.md)). |
| Configure → Server | **Search** | Built-in PostgreSQL search or Meilisearch ([Optional Meilisearch](../bloem/overlays/wiki/deployment/docker.md#optional-meilisearch)). Values set through `SILO_SEARCH_PROVIDER` and related variables lock these fields. |
| Configure → Server | **Network Access** | Overlay-network providers such as Tailscale. |
| Configure → Sign-in & Security | **Sign-in** | Silo passwords, public sign-ups, self-service password reset, single sign-on, network sign-in. |
| Configure → Sign-in & Security | **Security** | Which local servers the server may contact, rate limiting, API key tiers. |
| Manage → Libraries → Settings | **Library & Metadata** | Version listing, scanner and matcher workers, maturity limits, ratings, subtitle sync. |
| Manage → Autoscan → Settings | **Autoscan** | Polling defaults and connections. |
| Manage → Playback → Settings | **Playback** | Watch thresholds, transcoding and hardware acceleration, preview images, skip markers. |
| Manage → Downloads → Settings | **Downloads** | Whether viewers can download for offline use, per-user and server-wide limits, prepared copies. |
| Manage → Requests → Settings | **Requests** | Request limits and approval, Sonarr and Radarr servers, routing rules. |
| Configure → Integrations | **Providers** | Watch-provider plugins such as Simkl, subtitle provider accounts, the MDBList key, skip-marker providers. |
| Configure → Integrations | **Email** | The mail server for invitations, password resets and notification emails. |
| Configure → Integrations | **Notifications** | Delivery channels, server channels and how new content becomes notifications (2.15). |
| Configure → Integrations | **AI Services** | AI providers and the model each AI job uses (2.17). |
| Configure → Integrations | **Compatibility** | Jellyfin and Audiobookshelf app compatibility (2.12). |
| Configure → Integrations | **Announcements** | Bloem's page for publishing messages to viewers (2.16). |
| Monitor → Logs → Settings | **Logging** | Log level, quiet prefixes, log and permission-check retention. |

Metadata provider keys such as TMDB and TVDB belong to their plugins (2.11). Every setting has a
short explanation next to it. Pages stage your edits and nothing applies until you save with the
bar at the bottom of the page; a setting marked *Restart* needs a server restart to take effect.

### 2.8 Collections and sections

**Libraries → Collections** holds curated groups of items — "Christmas films", "The Marvel one in
order". You can build them by hand, with rules (a smart collection: "everything rated above 8 from
the 1990s"), or from a **collection template** that syncs a list from TMDB or MDBList and keeps it
current: [Collection Templates](../architecture/collection-templates.md) (Silo's page).

**Libraries → Home Sections** decides the rows on Home and on library pages — Continue watching,
Recently added, a collection, a recommendation row — and in what order. Viewers can hide rows they
do not want; you decide what is on offer.

### 2.9 Requests

Enable request access through the account/group policy and configure integrations,
routes, approval rules and quotas on **Requests → Settings**. **Requests → Queue** shows
the queue and its current delivery state. Viewers can follow an existing request
and select seasons where the title supports it; followers receive the relevant
updates. Bloem stamps requests with the acting organisation and bounds its
administration accordingly. See [request architecture](../architecture/media-requests.md)
(Silo's page) for routing, retries, fulfilment and group limits.

Bloem adds **Audiobooks** and **Ebooks** tabs beside **Movies & series** on both the queue and the
viewer's Requests page, when the server supports them. Each has its own connection setup on the
queue page and is off until configured. See [audiobook requests](../architecture/bloem-audiobook-requests.md)
and [ebook requests](../architecture/bloem-ebook-request-foundation.md).

### 2.10 Live TV

Live TV is Bloem's: Silo lists it as a non-goal. Bloem can tune, record and show a programme guide
from a network tuner or an authenticated Xtream live provider. The original tuner/DVR subsystem is
an attributed adaptation of Prairie Server (`ef2d38171`, 2026-08-13; see
[FORK.md](../../FORK.md#prairie-server-live-tv)); Xtream consumption is Bloem's addition
(2026-09-19). Sign in as an administrator, select your account's primary profile and complete any
required PIN verification, choose the **Platform** context, then open **Manage → Live TV**. Its tabs
are Tuners, Channels, Guide sources, Recordings and Transcoding.

- **Tuners.** Bloem talks to SiliconDust **HDHomeRun** tuners and to **Dispatcharr** (which makes
  IPTV sources look like an HDHomeRun). *Discover on LAN* finds tuners by broadcasting on your
  network; *Probe URL only* asks one address directly (`http://192.168.1.50`, or a Dispatcharr base
  such as `http://dispatcharr.local:9191`). Candidates are checked before you can add them.
- **Xtream providers.** In **Tuners → Xtream live TV provider**, enter a base URL without
  credentials, the provider username/password and an optional display name. Prefer HTTPS;
  HTTP sends provider credentials without TLS. The provider must support direct MPEG-TS
  without redirects. Only live channels are imported, not VOD or series.
- **Guide sources (EPG).** Programme listings from Schedules Direct, an XML sync (Gracenote) lineup
  or an Xtream provider's XMLTV guide. For Xtream, select the saved provider in **Xtream XMLTV
  guide**, choose **Add Xtream guide**, then use the source's **Sync** button. Do not paste an
  authenticated guide URL or another password. There is one Xtream guide per provider and a maximum
  of three enabled sources across all guide types.
- **Recordings (DVR)** and **Transcoding** for live streams, which cannot be prepared in advance the
  way a file can.

Xtream setup requires this server build to advertise support and the server's existing
`SECRET_KEY` to be available for encrypted storage. Upgrade every API/worker node before
adding providers and retain that key separately from the database backup. The connection
budget defaults to one (maximum 64), is capped by any positive provider limit, and is shared
by watching and recording across replicas. The same external account's use outside Bloem
is not included in that budget.

Credentials clear from the form on submission and are not shown again. If setup or guide
creation has an uncertain outcome, use **Reload providers** or **Reload guide sources** and
review the saved state before trying again; reopening the form is not permission to retry.
Rescans preserve channel identities and overrides. Missing channels become disabled;
returning channels remain disabled until reviewed. An empty rescan preserves the old lineup.

Removing a provider requires confirmation and is refused while physical streams are active.
It deletes its credentials, guide configuration, channels and related DVR entries, but not
recorded files. Credential, base-URL and budget editing are not available; removal/re-creation
is destructive to those entries. See [Xtream setup and limits](../architecture/xtream-live-tv.md).

> **Docker and LAN discovery.** The default Compose stack runs Bloem on a bridge network, which
> usually cannot send discovery broadcasts to your LAN, so *Discover on LAN* finds nothing. Either
> use *Probe URL only* with a reachable, permitted tuner address, or on Linux start with the Live TV
> override — `docker compose -f docker-compose.yml -f docker-compose.livetv.yml up -d` — which puts
> Bloem on the host network, where it listens on port 8080 instead of 8090. It combines with the
> GPU overrides. On Docker Desktop (Mac/Windows) host networking does not reach your real LAN; use
> *Probe URL only*.

Never expose unauthenticated HDHomeRun/Dispatcharr endpoints to the internet. The LAN discovery
override is not required for Xtream's outbound HTTP(S) requests. Full detail:
[Live TV tuner discovery](../livetv-tuner-discovery.md).

Viewers whose access group grants *Watch Live TV* can schedule from the guide or by channel and
time, and create recurring **Recording rules** from the Live TV page. Removing a rule does not
cancel already scheduled recordings or delete files. There is no rule editor or enforced retention
control. If readback after a recording write fails, actions stay blocked until **Reload recordings**
succeeds; review the result before submitting a fresh draft.

Completed recordings are not automatically imported into the catalog. If a recording
has no library link, make its folder available through a library and scan it; completion
alone does not make it playable in the catalog. Use the supported LAN topology for
tuners: normal URL and connection checks reject loopback destinations.

### 2.11 Plugins

Metadata providers (TMDB, TVDB and others), subtitle sources, skip-marker providers, request
routing, watch sync and storage providers are plugins. **Integrations → Plugins** lists what is
installed, lets you enable, disable and configure each one, checks for updates, and installs new
ones from the **Catalog** tab, from an added repository or from an uploaded plugin file. Most
metadata providers need an API key, entered on the plugin's own page and stored encrypted.

An installed plugin is trusted code: it runs as the server's own user, with the server's network
and file access, and is not sandboxed. Install only plugins you trust. See
[plugin trust](../architecture/plugin-trust.md) (Silo's page).

### 2.12 Native and compatibility clients

The web app and native client APIs share the main server address. Use a native
client release with matching server contracts; see [client contracts](../architecture/bloem-sdk-compatibility.md)
for the distinction between generated types and a tested app release.

Silo's two compatibility servers each listen on their own port, never on the main one, and are
configured under **Integrations → Compatibility**:

- **Jellyfin-compatible apps** (Infuse, Findroid, Swiftfin and others) use the Jellyfin port,
  `JF_PORT`, default `8096`, while **Allow Jellyfin apps to connect** is on. Set **Address Jellyfin
  apps should use** to the address those apps can reach.
- **Audiobookshelf apps** use `ABS_PORT`, default `13378`, while **Allow Audiobookshelf apps to
  connect** is on. Silo made this port configurable from the environment on 2026-10-26.

Both are on by default, and changing either switch needs a restart. Viewers sign in with
`account#profile` as the username; a PIN-protected profile appends `#PIN` to the password. Each
session sees only its organisation's libraries: Bloem wraps both servers' access checks with its
organisation scope. The viewer's **Settings → Connect Apps** page shows exactly what to type. See
[protocol compatibility](../architecture/bloem-compatibility-removal.md) for the tenant wiring and
persistence boundary.

### 2.13 Playback history, logs, diagnostics, tasks and maintenance

- **Playback → History** — finalized playback attempts across users and profiles, with the
  **Method** each used (direct or converted). It is the quickest way to see whether your devices are
  playing files natively. **Playback → Skip Markers** shows intro, recap and credits markers, and
  **Playback → Nodes** is described in 2.14.
- **Audio at playback start.** When a viewer starts something without choosing an audio track, the
  server starts from their language preference. Since Silo `e7605afe2` (merged 2026-10-27), if that
  track would have to be converted for the device, the server plays another main track in the same
  language that the device can take unconverted, with at least as many channels; commentary and
  audio-description tracks are never substituted. A track the viewer saved for a series still plays
  as saved.
- **Profiles own their playback.** A profile can report progress for and stop only its own playback,
  even on a shared account (Silo `e852571bb`). Another profile's request is refused with `403`.
- **Monitor → Logs** — **Server Log** (application, audit and FFmpeg output, filterable),
  **Client Diagnostics** (crash reports and debug bundles that apps upload from their own
  Diagnostics setting, each downloadable as a bundle) and **Settings** for logging. Signed stream
  and download tokens are masked in the server log (Silo #3137). When you ask for help, a filtered
  extract of the server log is what to attach; review it for personal data first.
- **Configure → Server → Scheduled Tasks** — every background job (scans, metadata refreshes,
  cleanups) with its schedule, progress and history. If a scan seems stuck, look here first.
- **Configure → Server → Maintenance** — catalog import and export (catalog seeds) and the job
  history of those runs.

There is no separate Stats page; the old `/admin/stats` address opens the Dashboard.

### 2.14 Nodes and distributed deployments

A single machine running everything is the *integrated* mode and is what you have after Part 1.
Bloem can also split into roles:

| Mode | What it does |
|---|---|
| `integrated` | Everything on one machine (the default). |
| `api` | The primary/control role for a custom distributed topology. |
| `transcode` | A worker that transcodes and prepares downloads, sharing the same database. |
| `proxy` | A stream and source-download delivery node. |

You would do this to put transcoding on the machine with the GPU while the database lives somewhere
quieter. **Playback → Nodes** shows each worker, its GPU, its scratch disk and its load, and holds
the routing choices for direct play, remux and transcoding;
[node observability](../architecture/observability.md#node-resource-sampling) explains resource sampling
and metrics. The Compose examples for each role are
in [Server roles and distributed deployments](../bloem/overlays/wiki/deployment/docker.md#server-roles-and-distributed-deployments).

### 2.15 Notifications and webhooks

**Integrations → Notifications** decides how the server delivers notifications and how new content
becomes one. Delivery channels are in-app, web push, the Silo push relay for the Apple and Android
apps, email (needs a mail server under **Integrations → Email**), and **Discord**. Viewers can also
add **personal webhooks**, within the limits you set here. **Server channels** announce new content
to a place of your choosing. Generic webhooks are signed with HMAC so the receiver can verify them,
and a receiver that keeps failing is automatically disabled rather than retried forever. See
[notifications](../architecture/notifications.md) (Silo's page).

### 2.16 Campaigns and seasonal packs

In **Platform** context, open **Campaigns & Seasonal Packs** to create, review, publish,
edit and delete campaigns or seasonal packs. Choose the audience, placements and dates;
use annual repeat only when the server offers it. Garden is not required for this web
workflow. Organisation administrators cannot publish through these controls.

The registries have no revision locking: a later save can overwrite an earlier one.
Reload before editing, review publication and confirm deletion. After a failed or
uncertain write, inspect the current list before deciding what to do next.

Artwork uploads accept PNG, JPEG, WebP or GIF up to 8 MiB. They are stored in the server's
artwork store, on local disk or S3 as configured under **Server → Storage**; with no store
configured at all, uploads are refused and you can still give an HTTPS artwork address. See
[campaign asset storage](../architecture/bloem-campaign-asset-storage.md) and
[S3 setup](../bloem/overlays/s3-storage-setup.md).

Home promotions default off and are hidden for children. Viewers can dismiss cards
and immediately continue past a pre-playback card. Seasonal effects respect reduced
motion, a device-local off switch, expiry and playback suppression. Signed-in Home
receives public packs plus those for the current organisation; sign-in shows public
packs only. See [web coverage](../architecture/bloem-web-feature-coverage.md) for
verified workflows and remaining browser/media acceptance.

**Integrations → Announcements** is the simpler, separate tool for publishing a message to viewers'
notifications and withdrawing it later.

### 2.17 AI Services and recommendation embeddings

AI features are optional and Silo's. **Integrations → AI Services** works in three parts, as Silo
reorganised it on 2026-10-26 (`c27acf246`, #3106) and Bloem merged on 2026-10-27:

- **Providers.** Connect each AI provider once: OpenAI, Gemini, OpenRouter, Groq, Ollama, a
  Whisper server, or any OpenAI-compatible endpoint, each with its base URL and API key. A stored key is sent
  only to the address saved with it, and a provider a job still uses cannot be removed. A ChatGPT
  plan can be signed in instead, for the text model only.
- **Models.** Each job picks a provider and a model from that list: the **Text model** (subtitle
  and description translation), **Speech-to-text** (subtitles generated from audio) and
  **Embeddings**. Speech-to-text no longer falls back to the text provider.
- **Features.** Translate subtitles, create subtitles from audio, translate descriptions, and the
  limits for each. A feature stays switched off until its model is configured.

On the first start after upgrading, the server converts the older single AI address and key, and
the separate speech address and key, into providers and binds each job to the one it used.

**Embeddings** power recommendations and semantic search. The AI Services page shows the current
choice read-only; choose the provider and model on **Integrations → Recommendations** (Silo
`39913c51d`, #3107), next to the embedding lock and the **Reset embeddings** action. The lock
follows the model, so the same model through another provider needs no reset. See
[AI providers](../architecture/ai-provider-authentication.md) and
[recommendations](../architecture/recommendations.md) (Silo's pages).

---

## Part 3 — Keeping it healthy

### 3.1 Backups

Back up three things, and test restoring them once:

1. **The PostgreSQL database.** Dump it with `pg_dump` from the host on a schedule; the
   [Docker guide](../bloem/overlays/wiki/deployment/docker.md#backups-and-updates) has the exact
   command. Do not copy the database directory while it is running. This is your watch history,
   users, settings, everything.
2. **`SILO_DATA_ROOT`** — artwork, plugins and prepared downloads. Losing artwork is recoverable
   (the server downloads it again) but slow.
3. **`.env`, above all `SECRET_KEY`** — separately from the database, as explained in 1.3.

Your media is not part of the backup; back that up however you already do.

### 3.2 Updating

Record the image you are running (`docker compose images silo`) and take a database backup first.
Then set `SILO_IMAGE` in `.env` to the new full-SHA tag and update only the application service:

```sh
cd bloem-server
git pull --ff-only
docker compose pull silo
docker compose up -d --no-deps silo
docker compose logs -f silo
```

Bloem migrates its own database on start, before it accepts connections. Read the
[integration changelog](../bloem/CHANGELOG.md) before an update; if a migration is mentioned, test
it on a copy first. Keep the same Compose project and override files for the update. For locally
built images, load the image and select its exact tag before `up`; do not try to pull a tag that
was never published. During migrations, follow the logs and allow startup to finish. Check both
`/api/v1/health` and `/api/v1/ready` afterwards.

Some migrations need every application, worker and scanner process stopped first, on every node:
the native storage onboarding migration (`20261006145307`) is one, so do not perform a rolling
upgrade across it. The [October 7 record](../operations/2026-10-07-native-storage-deployment.md)
documents one such rollout, and [Backups and updates](../bloem/overlays/wiki/deployment/docker.md#backups-and-updates)
lists the release-specific preparation.

### 3.3 When something is wrong

| Symptom | First thing to check |
|---|---|
| Films are missing after a scan | Folder and file naming — [the naming guide](../architecture/media-naming.md). Then **Libraries** for unmatched items and **Scheduled Tasks** for a scan error. |
| An item has the wrong artwork or title | Open it, choose **Match Item**, pick the right match. |
| Playback stutters or buffers | **Playback → History** — is it converting? If so, does the machine have a GPU, and is hardware acceleration set to *Auto*? |
| Hardware transcoding does not start | Check that the GPU override file is in use and the host driver (and, for NVIDIA, the container toolkit) is installed — [Hardware acceleration](../bloem/overlays/wiki/deployment/docker.md#hardware-acceleration). |
| A Jellyfin or Audiobookshelf app cannot connect | Check that the app uses the compatibility port (`8096` or `13378`), not 8090; that the switch under **Integrations → Compatibility** is on and the server was restarted; and that the username is `account#profile`. |
| Nobody can log in | Check **Silo public URL** under **Server → General** matches what people are typing, including `http` vs `https`. |
| The web app is slow to search | Enable Meilisearch under **Server → Search** — [Optional Meilisearch](../bloem/overlays/wiki/deployment/docker.md#optional-meilisearch). |
| The log warns "postgres restart required" on every start | Expected while PostgreSQL auto-tuning is on; it is not proof a restart is needed. See [the restart warning](../bloem/overlays/wiki/deployment/docker.md#the-restart-required-warning). With a PostgreSQL you manage yourself, set `POSTGRES_TUNE=off`. |
| The pulled image is old or missing features | Before October 27, 2026, `latest` was a September 24 build that Bloem's CI did not move. Pull again, or pin `SILO_IMAGE` to a full-SHA tag ([image selection](../bloem/overlays/wiki/deployment/docker.md#container-image-selection)). |

When you ask for help, give the image tag you run, the steps that fail, and a filtered extract of
**Monitor → Logs → Server Log** around the failure.

---

## Glossary

- **Access group** — a named set of permissions attached to users.
- **Administrative context** — whether you are administering the whole server (Platform) or one organisation.
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
- **Silo** — the upstream project Bloem tracks; see [Where Bloem comes from](#where-bloem-comes-from).
- **Tracking fork** — a fork that keeps merging its upstream instead of diverging from it.
- **Transcoding** — converting media on the fly so a device can play it.

## Source References

- [FORK.md](../../FORK.md) and the [integration changelog](../bloem/CHANGELOG.md) — what Bloem adds and when
- [Docker deployment](../bloem/overlays/wiki/deployment/docker.md) — the deployment reference this guide summarises
- [Xtream live providers](../architecture/xtream-live-tv.md) and the
  [September 19 deployment record](../operations/2026-09-19-xtream-deployment.md)
- `docs/architecture/bloem-compatibility-removal.md`, `docs/architecture/invitations-onboarding.md`,
  `docs/architecture/notifications.md`, `docs/architecture/ai-provider-authentication.md`
- `docs/livetv-tuner-discovery.md`, `docs/bloem/overlays/s3-storage-setup.md`
- `web/src/lib/adminNavigation.ts`, `web/src/lib/bloemAdminNavigation.ts` and
  `web/src/lib/adminSettingsSearch.ts` — the admin sidebar and settings pages named above
- `web/src/pages/setup-wizard/`, `web/src/pages/Admin*.tsx`, `web/src/pages/admin-*/`, `web/src/bloem/`
- `docker-compose.yml`, `docker-compose.livetv.yml`, `.env.bloem.example`
