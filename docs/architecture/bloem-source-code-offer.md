# How the source code offer works

> **Provenance and status.** Bloem addition, 2026-11-26. Bloem Server's repositories are
> private; the server itself offers its source code to the people who use it. Current.

## What it is for

Bloem Server is built on Silo, which is free software under the GNU Affero General Public
License, version 3 (AGPL). Bloem changes Silo, and people use the changed program over the
internet. Section 13 of the AGPL asks that every one of those people can get the complete
source code of the exact version they use, at no charge.

So the server carries its own source code. Anyone signed in can open **Settings, Source code**
and download it. The download is always the version the server runs at that moment: after
each update it is that update's code.

## What happens on an update

1. The deploy packs the commit it is about to ship into an archive,
   `bloem-server-<commit>.tar.gz`.
2. It copies the archive to every API server and checks it arrived intact, before any server
   switches to the new version. If the copy fails, the update stops.
3. The new server version looks for the archive named after its own commit. It offers that
   archive and no other. A few older archives stay beside it, so a rollback still has the
   source of the version it returns to.

## An example

Mira watches through a household login on her parents' account. She opens Settings, picks
**Source code** under About, and sees that the server runs version `4f2c1a9e0`, a 280 MB
download. She taps Download, and her browser saves `bloem-server-4f2c1a9e0….tar.gz`. A week
later the server is updated, and the same page offers the new version's archive.

## Who can change what

| Who | What they can do |
| --- | --- |
| Anyone signed in, household logins included | Read the version and download its source |
| The deploy | Writes the archive for each version it ships |
| An operator | Sets where the archives live (`BLOEM_SOURCE_ARCHIVE_DIR`) |

## Words used in the code

| Word | Meaning |
| --- | --- |
| offer | The running build's revision plus the archive directory (`internal/bloemsource`) |
| archive | `bloem-server-<revision>.tar.gz`, a `git archive` of that commit |
| revision | The full commit the binary was built from (`internal/buildinfo`) |

## For developers

- **Routes.** `GET /api/bloem/v2/source-code` describes the archive; `GET
  /api/bloem/v2/source-code/archive` sends it with range support. Both take any signed-in
  account: no profile, organization or admin context. A browser download passes the access
  token as `?token=`, as media URLs do. The handler clears the server's write deadline, since
  the archive is larger than the 120-second write timeout allows on a slow link.
- **Availability.** The routes mount, and `source_code_v1` joins the capability tokens,
  only when `BLOEM_SOURCE_ARCHIVE_DIR` is set and the build has a clean 40-character
  revision. A missing archive for the running revision answers `503 source_unavailable` and
  is logged at startup as an error.
- **Production.** The deploy tool writes archives to `/opt/silo-data/source` on each API
  server; the app container mounts that directory read-only at `/var/lib/silo/source`, and
  `BLOEM_SOURCE_ARCHIVE_DIR` points there. Never serve an archive whose name does not match
  the running revision: the offer must name the code that runs.
- **Web.** The settings page and its entry are Bloem-owned and reach Silo's settings through
  importer-scoped module swaps (`web/bloem-preview-plugin.ts`), not edits to Silo files.
