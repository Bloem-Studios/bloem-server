# Bloem overlay: Release versioning

**What this overlays.** Silo's [`docs/release-versioning.md`](../../release-versioning.md):
the section “Release versioning” (its opening paragraph about container tags)
and the section “Source of truth” (the container tag table). An *overlay* is a
Bloem-owned page that records Bloem's additions to, or replacements of, sections
of a Silo-owned document. The Silo file itself stays exactly as Silo wrote it, so
upstream merges stay clean; read it first, then this page. See the
[documentation workflow](../documentation-workflow.md).

**Why Bloem differs.** Silo's text describes Silo's own GitHub Releases and the
images Silo's `Docker Image` workflow publishes. Bloem is a separately
published fork with its own image workflow, its own prerelease and dated
deployment records. Bloem first noted these differences in the Silo file in
September 2026 (`75c1a1c2b`, 2026-09-01; `cc4b2ed98`, 2026-09-19) and moved them
here in `47ea7c889` (2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf`. Silo has not changed `docs/release-versioning.md` since the previous
review of this page (Silo `74158b4a8`); the Bloem facts below were rechecked.

Index: [Bloem documentation](../README.md).

---

**Location:** section “Release versioning”, the paragraph beginning “Silo uses
GitHub Releases as the public record of shipped…”, and section “Source of
truth”, the container tag table. **Bloem replaces** the container-tag
statements with:

## Bloem container images

Silo's three tags (`build-N`, `latest` and a short commit SHA) come from Silo's
`Docker Image` workflow, which runs after Silo's `CI` workflow. In Bloem's
repository Silo's `CI` job runs only when the repository is
`Silo-Server/silo-server`, so both workflows are skipped and publish nothing.

Since `04a69c0fe` (2026-10-11), Bloem images come from the
[Bloem Image workflow](../../../.github/workflows/bloem-image.yml). It runs only
after Bloem CI (`.github/workflows/bloem-ci.yml`) succeeds on a push to `main`, or
on a manual dispatch from `main` for a commit Bloem CI has already passed. It
checks out that exact commit, reuses the web build Bloem CI produced for it, and
publishes a single linux/amd64 image with a single tag:

| Tag | Meaning |
| --- | --- |
| `ghcr.io/bloem-studios/bloem-server:<full 40-character commit SHA>` | The exact `main` commit that passed Bloem CI. The image's `org.opencontainers.image.revision` label and the server's build revision carry the same SHA. |

The workflow does not move `latest` and creates no `build-N` or short-SHA tags.
The `latest` and `build-N` tags in Bloem's registry come from the publishing
path Bloem used before October 11; on 2026-10-27, `latest` was `build-152`, an
image from September 24, 2026. `docker-compose.yml` and `.env.bloem.example`
still default `SILO_IMAGE` to `latest`, so pin a full-SHA tag or a digest
instead. See the [Docker guide](wiki/deployment/docker.md#container-image-selection)
and [CI additions](../ci.md).

## Bloem releases

Bloem has one GitHub prerelease: `v0.1.0-bloem.20261011.1` (“Bloem Server storage
foundation release”), an annotated tag on `05c5f114e`, published 2026-10-11. It
follows Silo's SemVer format with a `-bloem.<date>.<n>` prerelease suffix. Its
assets are a Linux amd64 executable, the committed OpenAPI documents, release
metadata and checksums, not a container image. Silo itself has no release tags
yet. Neither Silo's `build-N` numbers nor Bloem's commit-SHA image tags are
SemVer releases.

## Bloem deployment records

Production deployments are recorded by revision, not by release. Each dated
record under [`docs/operations/`](../../operations/) gives the application
commit, the Silo revision it includes, the image and its digest, the server
binary checksum, migrations and the checks run, and it stays true to its date:

- From [October 27, 2026](../../operations/2026-10-27-main-deployment.md)
  (`422a51fed`, then `276becf4a`), production runs images that Bloem Image
  published, loaded on the application host and retagged
  `bloem-server:release-<short sha>`.
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
