# Bloem operator and viewer guides

This is Bloem's operator and viewer documentation index, a complete Bloem
edition rather than a section-by-section overlay. It began as Silo's
`docs/wiki/index.md`, which Bloem edited in place from August 2026 and moved here
in `47ea7c889` (2026-09-23). Silo then deleted its in-repository wiki in
`42b39765b` (#1635, 2026-09-28), when its operator guides moved out of the server
repository. Silo's current repository reference index is
[`docs/README.md`](../../../README.md). Bloem's complete index of the documents it
owns is [docs/bloem/README.md](../../README.md).

Reviewed on 2026-10-27 against Bloem `main` at `e852f92bf` (Silo through
`3e2d306b2`).

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
last_reviewed: 2026-10-27
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
  things, downloads, requests, notifications, and connecting supported native apps.

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
  authoring, viewer controls and where uploaded artwork is stored.
- [Artwork storage](../../../architecture/blob-storage.md) - Silo's catalog, branding and avatar storage;
  Bloem's campaign and seasonal uploads are in [Bloem artwork storage](admin/artwork-storage.md).
- [Organisation workflows](../../../wiki/admin-guide.md#26-entitlement-templates-organisations-and-policy) -
  Scoped grants, invitations and activity alongside policy management.
- [Entitlement Templates](../../../wiki/admin/entitlement-templates.md) - Create revisioned playback,
  download, profile, transcode, permission, and library policies and safely apply them to
  organizations or direct accounts.
- [Supported Media Folder Structures and Naming](../../../architecture/media-naming.md) - Accurate
  reference for the folder layouts and filenames Bloem can scan and match today.
- [Collection Templates](../../../architecture/collection-templates.md) - Curated, one-click starting points for
  synced library collections sourced from TMDB, Trakt, and MDBList.
- [Local NFO Metadata](../../../architecture/local-nfo-metadata.md) - Supported NFO sidecar fields, how they merge
  with online providers, and the naming-supplies-structure contract.
- [Node observability](../../../architecture/observability.md#node-resource-sampling) - CPU, memory,
  disk and GPU sampling for the Nodes page, and Prometheus metrics.

## Deployment

- [Deploy Bloem with Docker](deployment/docker.md) - Install and operate Bloem with Docker Compose,
  including storage, GPU acceleration, search, distributed roles, tuning, backups, and updates.
- [Integration changelog](../../CHANGELOG.md) - Current source integration, with deployment records kept separately.
- [Deployment records](../../../operations/) - Every dated deployment and integration record. The newest is the
  [October 27 main deployment](../../../operations/2026-10-27-main-deployment.md): Silo through `3e2d306b2`,
  the first CI-built production image, and PostgreSQL auto-tuning turned off. Each record is true for its date only.
- [October 7 native storage deployment](../../../operations/2026-10-07-native-storage-deployment.md) - Historical revision, migration checks and remaining verification.
- [September 30 upstream deployment](../../../operations/2026-09-30-upstream-deployment.md) - Historical membership migration and integration evidence.
- [September 19 deployment record](../../../operations/2026-09-19-xtream-deployment.md) - Applied
  migrations, operational smoke results, approved CI-timeout exception and rollback boundaries.

## Troubleshooting

- [Operator troubleshooting](../../../wiki/admin-guide.md#33-when-something-is-wrong) - Startup, media paths, playback and storage.
- [Contract transition](../../../architecture/bloem-contract-adjudications.md) - Native-client adoption checks before rollout.
- [Upgrade validation](../../../operations/2026-10-07-native-storage-deployment.md#outstanding-validation) - Historical provider/client acceptance limits, distinct from live health.

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
