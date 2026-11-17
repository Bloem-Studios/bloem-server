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
last_reviewed: 2026-11-16
related: []
---

# Bloem guides for operators and viewers

This is Bloem's index of the guides for people who run a Bloem server and people who watch
on one. Silo's repository does not keep these guides, so this index is Bloem's own
edition.

This page lists the guides by who they are for: viewers first, then the person running the
server, then installing and updating, then what to do when something goes wrong. Each guide is
plain Markdown, so it reads the same on GitHub and in a synced wiki.

## Start here

- [Bloem Server admin guide](../../../wiki/admin-guide.md): from a blank machine to a household
  streaming its own library. Install, first run, libraries, users and profiles, playback,
  organizations and their own addresses, plans, maintenance and troubleshooting, written for a
  first-time operator.
- [Bloem user guide](../../../wiki/user-guide.md): for viewers. Signing in, profiles, finding and
  playing things, downloads, requests, notifications, and connecting supported apps.

## For viewers

- [Getting in](../../../wiki/user-guide.md#1-getting-in): invitations, the server address, signing
  in on another organization's address, and being asked to sign in again.
- [Profiles](../../../wiki/user-guide.md#2-profiles): managing a household, and why everyone
  signs in to the account.
- [Watching](../../../wiki/user-guide.md#4-watching): player controls, subtitles, quality,
  audiobooks, the reader and watching together.
- [Live TV and recordings](../../../wiki/user-guide.md#live-tv-and-recordings): scheduling from the
  guide or by channel and time, recording rules, what to do after an uncertain result, and why a
  recording needs a library scan.

## For the person running the server

How it works, in plain language with pictures:

- [How organizations work](../../../architecture/bloem-organizations.md): the platform,
  organizations, accounts and profiles.
- [Organization domains and branding](../../../architecture/bloem-organization-domains.md): an
  organization's own addresses, name, logo and colours.
- [How plans work](../../../architecture/bloem-plans.md): plans, previews and the upgrade prompt.
- [How Live TV works](../../../architecture/bloem-live-tv.md),
  [How storage sources work](../../../architecture/bloem-storage-sources.md),
  [How requests work](../../../architecture/bloem-requests.md),
  [How messages and promotions work](../../../architecture/bloem-messages-and-promotions.md) and
  [How apps connect](../../../architecture/bloem-apps.md).

Tasks in the admin guide:

- [Organization domains and branding](../../../wiki/admin-guide.md#218-organization-domains-and-branding):
  adding a domain, the provider addresses to register for it, and the Domains and Branding panels.
- [Organization workflows](../../../wiki/admin-guide.md#26-plans-organizations-and-policy): scoped
  grants, invitations and activity, beside plan management.
- [Live TV and Xtream setup](../../../wiki/admin-guide.md#210-live-tv): encrypted providers, XMLTV,
  shared connection budgets, recovering from an uncertain result, and what removing a provider
  deletes.
- [Campaigns and seasonal packs](../../../wiki/admin-guide.md#216-campaigns-and-seasonal-packs):
  platform authoring, viewer controls and where uploaded artwork is stored.

Reference:

- [Entitlement Templates](../../../wiki/admin/entitlement-templates.md): create revisioned playback,
  download, profile, transcode, permission and library plans, and apply them safely to
  organizations or direct accounts.
- [Artwork storage](../../../architecture/blob-storage.md): Silo's catalog, branding and avatar
  storage. Bloem's campaign and seasonal uploads are in [Bloem artwork storage](admin/artwork-storage.md).
- [Supported Media Folder Structures and Naming](../../../architecture/media-naming.md): the folder
  layouts and file names Bloem can scan and match today.
- [Collection Templates](../../../architecture/collection-templates.md): curated, one-click starting
  points for synced library collections from TMDB, Trakt and MDBList.
- [Local NFO Metadata](../../../architecture/local-nfo-metadata.md): supported NFO sidecar fields, how
  they merge with online providers, and the rule that naming supplies the structure.
- [Node observability](../../../architecture/observability.md#node-resource-sampling): CPU, memory,
  disk and GPU sampling for the Nodes page, and Prometheus metrics.

## Installing and updating

- [Deploy Bloem with Docker](deployment/docker.md): install and run Bloem with Docker Compose,
  including storage, GPU acceleration, search, distributed roles, tuning, backups and updates.
- [Release versioning](../release-versioning.md): which image tags Bloem publishes (`latest`,
  dated tags, `edge` and one per commit) and how to pin one.

## When something goes wrong

- [Operator troubleshooting](../../../wiki/admin-guide.md#33-when-something-is-wrong): startup, media
  paths, playback, storage, and sign-in on organization addresses.
- [Viewer troubleshooting](../../../wiki/user-guide.md#10-when-something-is-not-working): connecting
  apps, passwords, playback and sign-in messages.

## For developers

This index started as Silo's `docs/wiki/index.md`. Silo has since deleted its in-repository
wiki, so this is a complete Bloem edition, not a section-by-section overlay. Silo's current
reference index is [`docs/README.md`](../../../README.md). Bloem's complete index of the documents
it owns is [docs/bloem/README.md](../../README.md).

### Editing rules

- Keep pages in `docs/wiki/` digestible for the intended audience.
- Prefer updating existing pages over creating duplicates.
- Use YAML frontmatter at the top of the file, and portable Markdown, so pages render cleanly on
  GitHub and sync into Wiki.js without rewriting.
- Add a `## Source References` section instead of copying code into docs. In a page with a For
  developers section, the source references list goes inside it.
- Reserve `docs/wiki/` for end-user and operator docs. Keep architecture material in
  `docs/architecture/`.
- When a page is added, add bullet entries to the matching section here (replacing a
  `No pages yet.` line, if the section has one): a page-title link to the relative Markdown file,
  followed by a one-line summary.
- When a section already has pages, append a new bullet instead of adding prose.
