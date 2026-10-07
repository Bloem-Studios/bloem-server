# Bloem overlay: Release versioning

Bloem additions and overrides for the upstream Silo document [`docs/release-versioning.md`](../../release-versioning.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Release versioning”, after the paragraph beginning “Silo uses GitHub Releases as the public record of shipped…”. **Bloem adds:**

## Bloem deployment records

The upstream release conventions remain distinct from a Bloem deployment.
The [October 7, 2026 deployment](../../operations/2026-10-07-native-storage-deployment.md)
uses Bloem commit `c87b44545`, including Silo upstream `74158b4a8`.
`bloem-server:native-onboarding-c87b44545` was built and loaded on the deployment
host; that local tag does not assert that the same tag exists in GHCR. Source is
published on `codex/native-storage-20261006`; this is not a claim that `main` or
the registry's `latest` tag moved.

Silo's `build-1033` identifies a separately published Silo image. Neither that
build number nor Bloem's revision-specific deployment tag is a new SemVer release.
Record the application revision, included upstream revision and actual image
digest separately. A successful `latest` pull must be compared with the running
container before claiming an installation is current.

The [September 19 record](../../operations/2026-09-19-xtream-deployment.md) and its
CI exception remain historical. The September 30 record also records incomplete
full-suite validation; neither is a general testing or downgrade exemption.

Later documentation commits do not change the running application image.
