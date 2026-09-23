# Bloem overlay: Silo Wiki

This is Bloem's complete edition of the upstream Silo document [`docs/wiki/index.md`](../../../wiki/index.md),
which is kept verbatim so Silo merges stay clean. For Bloem Server, read this
edition instead. Index: [Bloem documentation](../../README.md).

---
title: Bloem Wiki
description: User-facing Bloem documentation written in Markdown for in-repo use and Wiki.js sync.
summary: Index of digestible Bloem docs by audience and subject.
tags:
  - bloem
  - docs
  - wiki
audience:
  - end-user
  - operator
last_reviewed: 2026-09-19
related: []
---

# Bloem Wiki

This directory is the repo-local home for user-digestible Bloem documentation. Pages here
should stay portable Markdown so they can render cleanly in GitHub and sync into Wiki.js without
rewriting.

## Sections

## Getting Started

- [Bloem Server Admin Guide](../../../wiki/admin-guide.md) - From a blank machine to a household streaming their
  own library: install, first run, libraries, users and profiles, playback, access policy,
  maintenance, and troubleshooting, written for a first-time operator.
- [Bloem User Guide](../../../wiki/user-guide.md) - The viewer's guide: signing in, profiles, finding and playing
  things, downloads, requests, notifications, and using Jellyfin/Emby/Audiobookshelf apps with a
  Bloem server.

## Features

- [Bloem User Guide → Watching](../../../wiki/user-guide.md#4-watching) - Player controls, subtitles, quality,
  audiobooks, the reader, and watching together, by device.
- [Live TV and recordings](../../../wiki/user-guide.md#live-tv-and-recordings) - Guide/manual scheduling,
  series rules, recovery after uncertain writes and manual library import.
- [Profile credentials](../../../wiki/user-guide.md#2-profiles) - Household management and the account-login
  requirement in the browser.

## Admin

- [Live TV and Xtream setup](../../../wiki/admin-guide.md#210-live-tv) - Encrypted providers, XMLTV,
  shared connection budgets, uncertain-write recovery and provider-removal consequences.
- [Campaigns and seasonal packs](../../../wiki/admin-guide.md#216-campaigns-and-seasonal-packs) - Platform
  authoring, viewer controls and public-S3 upload requirements.
- [Artwork storage](../../../wiki/admin/blob-storage.md) - Catalog, avatar and engagement storage boundaries.
- [Organisation workflows](../../../wiki/admin-guide.md#26-entitlement-templates-organisations-and-policy) -
  Scoped grants, invitations and activity alongside policy management.
- [Entitlement Templates](../../../wiki/admin/entitlement-templates.md) - Create revisioned playback,
  download, profile, transcode, permission, and library policies and safely apply them to
  organizations or direct accounts.
- [Supported Media Folder Structures and Naming](../../../wiki/admin/media-folder-and-naming.md) - Accurate
  reference for the folder layouts and filenames Bloem can scan and match today.
- [Collection Templates](../../../wiki/admin/collection-templates.md) - Curated, one-click starting points for
  synced library collections sourced from TMDB, Trakt, and MDBList.
- [Local NFO Metadata](../../../wiki/admin/nfo-local-metadata.md) - Supported NFO sidecar fields, how they merge
  with online providers, and the naming-supplies-structure contract.
- [Monitoring Stream Nodes](../../../wiki/admin/monitoring-nodes.md) - Reading the Nodes page columns, re-probing
  a node's GPU after a driver change, scratch-disk admission, and scraping node metrics.

## Deployment

- [Deploy Bloem with Docker](../../../wiki/deployment/docker.md) - Install and operate Bloem with Docker Compose,
  including storage, GPU acceleration, search, distributed roles, tuning, backups, and updates.
- [September 19 deployment record](../../../operations/2026-09-19-xtream-deployment.md) - Applied
  migrations, operational smoke results, approved CI-timeout exception and rollback boundaries.

## Troubleshooting

- No pages yet.

## Editing Rules

- Keep pages in `docs/wiki/` digestible for the intended audience.
- Prefer updating existing pages over creating duplicates.
- Use YAML frontmatter and portable Markdown.
- Add `## Source References` sections instead of copying code into docs.
- Reserve `docs/wiki/` for end-user and operator docs. Keep architecture material in
  `docs/architecture/`.
- When a page is added, replace the matching `No pages yet.` line with bullet entries in this form:
  a page-title link to the relative Markdown file, followed by a one-line summary.
- When a section already has pages, append a new bullet instead of adding prose.
