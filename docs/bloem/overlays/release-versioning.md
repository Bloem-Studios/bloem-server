# Bloem overlay: Release versioning

This overlay covers Silo's [`docs/release-versioning.md`](../../release-versioning.md), which stays exactly as Silo wrote it.

Silo's page explains how Silo numbers its releases and tags its container images.
Bloem is published separately, from its own repository, so its images and its one
release work differently. Everything else on Silo's page, such as backing up before
an upgrade and the version format, applies to Bloem as written.

![How a Bloem commit becomes an image: a commit on main passes Bloem CI, which builds no image; Bloem's deploy tool builds the image for the commit it deploys, and after the deploy passes verification publishes that one image as release-<sha9>, the full commit SHA, latest and a dated tag such as 2026.11.26. Commits that are never deployed get no image. edge no longer moves and stays at 9fa2dad01. Silo's own build-N tags are not published for Bloem](../../architecture/images/overlay-release-versioning/images.svg)

## What Bloem changes

- **Images are built at deploy time.** Bloem CI tests the commits on `main` but
  builds no image. Bloem's deploy tool builds the image for the commit it deploys and
  publishes it only after the deploy has passed its checks. A commit that is never
  deployed gets no image.
- **One image, four tags.** A deployed commit's image is tagged with the full
  commit it was built from, with `release-` and the first nine characters of that
  commit, with `latest`, and with a dated tag such as `2026.11.26`. All four name the
  same image. There are no build numbers.
- **`latest` follows production.** It moves only after a verified deploy, and
  never back to an older build. Each such release also gets a dated tag
  (`2026.11.26.2`, `.3` for later releases the same day) that never moves.
- **`edge` no longer moves.** It stays at `9fa2dad01`, the last image the
  now-disabled Bloem Image workflow published. If you pull `edge`, switch to
  `latest` or a dated tag.
- **GitHub releases are rare.** Bloem has published one prerelease. An installation is
  identified by its commit or its dated tag, not by a release number.

## Example: a day with two deploys

This example is made up.

1. In the morning a maintainer deploys commit `4c1d0e7b2…`. The deploy tool builds
   its image, the new build passes its checks in production, and the image is
   published as `release-4c1d0e7b2`, `4c1d0e7b2…` in full, `latest` and
   `2026.11.26`.
2. Three more commits land on `main` and pass Bloem CI. None is deployed, so none
   gets an image.
3. In the evening the newest of them, `a83f6c210…`, is deployed. Its image takes
   `latest` and `2026.11.26.2`. `2026.11.26` still names the morning's image.
4. Someone who pinned `2026.11.26` stays on the morning's build until they change
   the tag.

## Words used in the code

| On this page | In the code and registry |
| --- | --- |
| The image for a deployed commit | `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` and `:release-<sha9>` |
| `latest`, a dated tag | `ghcr.io/bloem-studios/bloem-server:latest`, `:2026.11.26` |
| `edge`, no longer moved | `ghcr.io/bloem-studios/bloem-server:edge`, at `9fa2dad01` |
| The deploy tool | `bloem-deploy`, which runs outside this repository |
| The disabled workflows | [Bloem Image](../../../.github/workflows/bloem-image.yml) (`bloem-image.yml`) and [Bloem Release](../../../.github/workflows/bloem-release.yml) (`bloem-release.yml`) |

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

Bloem images are built only when a commit is deployed, by Bloem's deploy tool,
`bloem-deploy`, which runs outside this repository and outside GitHub Actions. It
normally deploys only a commit that passed Bloem CI; an operator can override that
check. It builds a single linux/amd64 image of
the server alone, with the web app it builds from the same commit. PostgreSQL, Redis
and Meilisearch run as their own containers (`docker-compose.advanced.yml`). After the
deploy passes verification it pushes the image to Bloem's private registry, then
copies that same manifest, with an identical digest, to the public
`ghcr.io/bloem-studios/bloem-server`, where it carries these tags:

| Tag | Meaning |
| --- | --- |
| `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` | The exact deployed commit. The image's `org.opencontainers.image.revision` label and the server's build revision carry the same SHA. |
| `ghcr.io/bloem-studios/bloem-server:release-<sha9>` | The same image, under the first nine characters of the commit, as production names it. |
| `ghcr.io/bloem-studios/bloem-server:latest` | The image Bloem's production runs. It moves only after a verified deploy. Publishing an older deploy again leaves it where it is when a later build has gone live or that deploy was rolled back, so it never moves backwards. |
| `ghcr.io/bloem-studios/bloem-server:2026.11.26` | A dated tag: the deploy's UTC day, then `.2`, `.3` for later releases that day. A dated tag that already holds another image is never moved, so pin one to choose when you update. |
| `ghcr.io/bloem-studios/bloem-server:edge` | Frozen at `9fa2dad01`, the last image Bloem Image published. Nothing moves it now. |

The GitHub workflows that used to publish images, Bloem Image
(`.github/workflows/bloem-image.yml`) and Bloem Release
(`.github/workflows/bloem-release.yml`), are disabled on GitHub since 2026-11-26.
Bloem Image built an image for every `main` commit that passed Bloem CI and moved
`edge`; Bloem Release pointed `latest` and a dated tag at one of those images after a
deploy. [Bloem CI](../ci.md#bloem-image-disabled) keeps a short record of both.

No `build-N` tags are made. `.env.bloem.example` sets `SILO_IMAGE` to `latest` and
selects `docker-compose.advanced.yml`, the Compose file that reads it; Silo's
`docker-compose.yml` starts Silo's own image. As Silo's table says of its own
`latest`, an installation that must be rolled back or attributed pins a dated tag, a
full-SHA tag or a digest instead. See the
[Docker guide](wiki/deployment/docker.md#container-image-selection) and
[Bloem CI](../ci.md#how-the-server-image-is-published).

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

A commit that is never deployed, such as a documentation-only commit followed by
another before the next deploy, has no image. The next deployed image contains it.

### Related pages

- [Bloem CI](../ci.md#how-the-server-image-is-published): how a deploy builds and tags an image.
- [Docker deployment guide](wiki/deployment/docker.md#container-image-selection):
  choosing and pinning an image.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).

The picture is drawn by `scripts/bloem/pictures/overlay_release_versioning.py`
into `docs/architecture/images/overlay-release-versioning/`. Redraw it there, not
by hand.
