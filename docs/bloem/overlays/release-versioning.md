# Bloem overlay: Release versioning

This overlay covers Silo's [`docs/release-versioning.md`](../../release-versioning.md), which stays exactly as Silo wrote it.

Silo's page explains how Silo numbers its releases and tags its container images.
Bloem is published separately, from its own repository, so its images and its one
release work differently. Everything else on Silo's page, such as backing up before
an upgrade and the version format, applies to Bloem as written.

![How a Bloem commit becomes an image: a commit on main passes Bloem CI, Bloem Image publishes an image tagged with the full commit SHA and moves edge while that commit is main's newest; after a deploy of that commit passes verification, Bloem Release points latest and a dated tag such as 2026.11.17 at the same image. Silo's own build-N tags are not published for Bloem](../../architecture/images/overlay-release-versioning/images.svg)

## What Bloem changes

- **One image per commit.** Every Bloem image is tagged with the full commit it was
  built from. There are no build numbers.
- **`edge` follows `main`.** It moves to a new image only while that image's commit is
  still the newest on `main`.
- **`latest` is what Bloem's own production runs.** It moves after a verified
  deploy, at most about once a day. Each such release also gets a dated tag such as
  `2026.11.17` (`2026.11.17.2` for a second one the same day) that never moves.
- **GitHub releases are rare.** Bloem has published one prerelease. An installation is
  identified by its commit or its dated tag, not by a release number.

## Words used in the code

| On this page | In the code and registry |
| --- | --- |
| The image for a commit | `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` |
| `edge`, `latest`, a dated tag | `ghcr.io/bloem-studios/bloem-server:edge`, `:latest`, `:2026.11.17` |
| Publishing workflow | [Bloem Image](../../../.github/workflows/bloem-image.yml) (`bloem-image.yml`) |
| Release workflow | [Bloem Release](../../../.github/workflows/bloem-release.yml) (`bloem-release.yml`) |

## For developers

### Section by section

---

**Location:** section “Release versioning”, the paragraph beginning “Silo uses
GitHub Releases as the public record of shipped…”, and section “Source of
truth”, the container tag table and the paragraph after it beginning “The image
bundles PostgreSQL, Redis, and Meilisearch.”. **Bloem replaces** the
container-tag statements with:

#### Bloem container images

Silo's tags (`build-N`, `latest` and the full commit SHA) come from Silo's
`Docker Image` workflow, which runs after Silo's `CI` workflow and publishes Silo's
all-in-one image, with PostgreSQL, Redis and Meilisearch bundled. In Bloem's
repository Silo's `CI` job runs only when the repository is
`Silo-Server/silo-server`, so both workflows are skipped and publish nothing.

Bloem images come from the [Bloem Image workflow](../../../.github/workflows/bloem-image.yml).
It runs only after Bloem CI (`.github/workflows/bloem-ci.yml`) succeeds on a push to
`main`, or on a manual dispatch from `main` for a commit Bloem CI has already passed.
It checks out that exact commit, reuses the web build Bloem CI produced for it, and
publishes a single linux/amd64 image of the server alone. PostgreSQL, Redis and
Meilisearch run as their own containers (`docker-compose.advanced.yml`). The images
are public and carry these tags:

| Tag | Meaning |
| --- | --- |
| `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` | The exact `main` commit that passed Bloem CI. The image's `org.opencontainers.image.revision` label and the server's build revision carry the same SHA. |
| `ghcr.io/bloem-studios/bloem-server:edge` | The newest of those images whose commit was still `main`'s head when it was pushed. |
| `ghcr.io/bloem-studios/bloem-server:latest` | The image Bloem's production runs. [Bloem Release](../../../.github/workflows/bloem-release.yml) points it at a commit's image after a deploy of that commit has been verified. |
| `ghcr.io/bloem-studios/bloem-server:2026.11.17` | A dated tag made by the same release step. It never moves, so pin it to choose when you update. |

The workflows create no `build-N` or short-SHA tags. `.env.bloem.example` sets
`SILO_IMAGE` to `latest` and selects `docker-compose.advanced.yml`, the Compose file
that reads it; Silo's `docker-compose.yml` starts Silo's own image. As Silo's table
says of its own `latest`, an installation that must be rolled back or attributed
pins a dated tag, a full-SHA tag or a digest instead. See the
[Docker guide](wiki/deployment/docker.md#container-image-selection) and
[Bloem CI](../ci.md#bloem-image).

#### Bloem releases

Bloem has one GitHub prerelease: `v0.1.0-bloem.20261011.1` (“Bloem Server storage
foundation release”), published 2026-10-11. It follows Silo's SemVer format with a
`-bloem.<date>.<n>` prerelease suffix. Its assets are a Linux amd64 executable, the
committed OpenAPI documents, release metadata and checksums, not a container image.
Silo itself has no release tags yet. Neither Silo's `build-N` numbers nor Bloem's
commit-SHA image tags are SemVer releases.

#### Knowing what an installation runs

A successful `latest` pull does not show that an installation is current: compare the
running container's revision (its `org.opencontainers.image.revision` label, or the
build revision the server reports) with the commit you expect. Record the application
revision, the Silo revision it includes and the image digest separately.

A documentation-only commit on `main` does not change the running application
image, although Bloem Image still publishes an image for it.

### Related pages

- [Bloem CI](../ci.md#bloem-image): how Bloem Image builds and tags an image.
- [Docker deployment guide](wiki/deployment/docker.md#container-image-selection):
  choosing and pinning an image.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_release_versioning.py`
into `docs/architecture/images/overlay-release-versioning/`. Redraw it there, not
by hand.
