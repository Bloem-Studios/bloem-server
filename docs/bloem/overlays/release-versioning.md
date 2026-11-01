# Bloem overlay: Release versioning

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> replaces in Silo's [`docs/release-versioning.md`](../../release-versioning.md),
> which stays exactly as Silo wrote it. The replacement is in the
> [For developers](#for-developers) section.

Silo's page explains how Silo numbers its releases and tags its container images.
Bloem is published separately, from its own repository, so its images, its one
release and its deployment records work differently. Everything else on Silo's
page, such as backing up before an upgrade and the version format, applies to Bloem
as written.

![How a Bloem commit reaches production: a commit on main passes Bloem CI, Bloem Image publishes an image tagged with the full commit SHA and moves latest while that commit is main's newest; or, when the owner asks, the image is built locally from the exact commit. Either way production runs it as bloem-server:release-short-sha, and a dated record notes the commit, the Silo revision and the image digest. Silo's own build-N tags are not published for Bloem](../../architecture/images/overlay-release-versioning/images.svg)

## What Bloem changes

- **One tag per commit.** Every Bloem image is tagged with the full commit it was
  built from. There are no build numbers.
- **`latest` follows `main`.** It moves to a new image only while that image's
  commit is still the newest on `main`.
- **Releases are rare.** Bloem has published one prerelease. A deployment is
  identified by its commit, not by a release number.
- **Each deployment has a dated record.** It names the Bloem commit, the Silo
  revision inside it and the exact image.

## Words used in the code

| On this page | In the code and registry |
| --- | --- |
| The image for a commit | `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` |
| `latest` | `ghcr.io/bloem-studios/bloem-server:latest` |
| The image production runs | `bloem-server:release-<short sha>`, a local tag on the deployment hosts |
| Publishing workflow | [Bloem Image](../../../.github/workflows/bloem-image.yml) (`bloem-image.yml`) |
| Dated record | `docs/operations/<date>-<topic>.md` |

## For developers

### Where this overlay came from

Silo's text describes Silo's own GitHub Releases and the images Silo's
`Docker Image` workflow publishes. Bloem first noted its differences in the Silo
file in September 2026 (`75c1a1c2b`, 2026-09-01; `cc4b2ed98`, 2026-09-19) and moved
them here in `47ea7c889` (2026-09-23). It was reviewed on 2026-10-27 against Silo
`3e2d306b2` and Bloem `main` at `e852f92bf`.

From `3e2d306b2` through `63ef34b23` Silo changed `docs/release-versioning.md`
twice: `577386316` (#3118, 2026-10-28) added an all-in-one image with PostgreSQL,
Redis and Meilisearch bundled, and `c3115d493` (#3319, 2026-10-31) made it the one
image Silo publishes under the plain tags, which are now `build-N`, `latest` and the
full commit SHA. This rewrite updated Silo's tags below, added the paragraph on the
bundled image to the passage Bloem replaces, and brought the deployment records up
to November 1.

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

Since `04a69c0fe` (2026-10-11), Bloem images come from the
[Bloem Image workflow](../../../.github/workflows/bloem-image.yml). It runs only
after Bloem CI (`.github/workflows/bloem-ci.yml`) succeeds on a push to `main`, or
on a manual dispatch from `main` for a commit Bloem CI has already passed. It
checks out that exact commit, reuses the web build Bloem CI produced for it, and
publishes a single linux/amd64 image of the server alone. PostgreSQL, Redis and
Meilisearch run as their own containers (`docker-compose.advanced.yml`). The image
has these tags:

| Tag | Meaning |
| --- | --- |
| `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` | The exact `main` commit that passed Bloem CI. The image's `org.opencontainers.image.revision` label and the server's build revision carry the same SHA. |
| `ghcr.io/bloem-studios/bloem-server:latest` | Since `6d0adcfe9` (2026-10-27), the newest of those images whose commit was still `main`'s head when it was pushed. |

The workflow creates no `build-N` or short-SHA tags. Before `6d0adcfe9` it did
not move `latest` either: the `latest` and `build-N` tags came from the
publishing path Bloem used before October 11, and on 2026-10-27 `latest` was
still `build-152`, an image from September 24, 2026, until the first run after
that change moved it. `.env.bloem.example` sets `SILO_IMAGE` to `latest` and
selects `docker-compose.advanced.yml`, the Compose file that reads it; Silo's
`docker-compose.yml` starts Silo's own image. As Silo's table says of its own `latest`, a
deployment that must be rolled back or attributed pins a full-SHA tag or a
digest instead. See the [Docker guide](wiki/deployment/docker.md#container-image-selection)
and [Bloem CI](../ci.md#bloem-image).

#### Bloem releases

Bloem has one GitHub prerelease: `v0.1.0-bloem.20261011.1` (“Bloem Server storage
foundation release”), an annotated tag on `05c5f114e`, published 2026-10-11. It
follows Silo's SemVer format with a `-bloem.<date>.<n>` prerelease suffix. Its
assets are a Linux amd64 executable, the committed OpenAPI documents, release
metadata and checksums, not a container image. Silo itself has no release tags
yet. Neither Silo's `build-N` numbers nor Bloem's commit-SHA image tags are
SemVer releases.

#### Bloem deployment records

Production deployments are recorded by revision, not by release. Each dated
record under [`docs/operations/`](../../operations/) gives the application
commit, the Silo revision it includes, the image and its digest, the server
binary checksum, migrations and the checks run, and it stays true to its date.
Production runs every image as `bloem-server:release-<short sha>`, and the image
reached it in one of two ways:

- **Published by Bloem Image**, loaded on the deployment hosts and retagged:
  from [October 27, 2026](../../operations/2026-10-27-main-deployment.md)
  (`422a51fed`, then `276becf4a`, `7a3b59027`, `bee8a7728` and `aeeea2357`), the
  [native audiobook scans](../../operations/2026-10-28-native-audio-deployment.md)
  and the [native scan throughput](../../operations/2026-10-28-native-scan-throughput-deployment.md)
  deployments of October 28.
- **Built locally**, at the owner's request, from a `git archive` of the exact
  commit with Bloem Image's build arguments, before Bloem CI finished:
  `9cc80cd33` on the morning of October 28 (a
  [follow-up](../../operations/2026-10-27-main-deployment.md#follow-up-9cc80cd33)
  in the October 27 record), the
  [native scan redesign](../../operations/2026-10-28-native-scan-redesign-deployment.md)
  of October 28 and 29, and every deployment from
  [October 29](../../operations/2026-10-29-silo-sync-free-play-deployment.md)
  through [November 1](../../operations/2026-11-01-provider-sign-out-deployment.md).

Earlier deployments:

- The [October 26](../../operations/2026-10-26-main-deployment.md) and
  [October 26 Stage A](../../operations/2026-10-26-stage-a-deployment.md)
  deployments used images built locally from a `git archive` of the source.
- The [October 7, 2026 deployment](../../operations/2026-10-07-native-storage-deployment.md)
  used Bloem commit `c87b44545` with Silo `74158b4a8`. Its
  `bloem-server:native-onboarding-c87b44545` was a local tag on the deployment
  host and does not assert a matching GHCR tag; that source was published on a
  feature branch, not `main`.

Record the application revision, the included Silo revision and the actual
image digest separately. A successful `latest` pull does not show that an
installation is current: compare the running container's revision with the
intended commit. The [September 19 record](../../operations/2026-09-19-xtream-deployment.md)
and its CI exception, and the incomplete full-suite validation the
[September 30 record](../../operations/2026-09-30-upstream-deployment.md)
notes, remain historical; neither is a general testing or downgrade exemption.

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
