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
last_reviewed: 2026-08-20
related: []
---

# Bloem Wiki

This directory is the repo-local home for user-digestible Bloem documentation. Pages here
should stay portable Markdown so they can render cleanly in GitHub and sync into Wiki.js without
rewriting.

## Sections

## Getting Started

- No pages yet.

## Features

- No pages yet.

## Admin

- [Entitlement Templates](admin/entitlement-templates.md) - Create revisioned playback,
  download, profile, transcode, permission, and library policies and safely apply them to
  organizations or direct accounts.
- [Supported Media Folder Structures and Naming](admin/media-folder-and-naming.md) - Accurate
  reference for the folder layouts and filenames Bloem can scan and match today.
- [Collection Templates](admin/collection-templates.md) - Curated, one-click starting points for
  synced library collections sourced from TMDB, Trakt, and MDBList.
- [Local NFO Metadata](admin/nfo-local-metadata.md) - Supported NFO sidecar fields, how they merge
  with online providers, and the naming-supplies-structure contract.

## Deployment

- [Deploy Bloem with Docker](deployment/docker.md) - Install and operate Bloem with Docker Compose,
  including storage, GPU acceleration, search, distributed roles, tuning, backups, and updates.

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
  `- [Page Title](section/file.md) - one-line summary`
- When a section already has pages, append a new bullet instead of adding prose.
