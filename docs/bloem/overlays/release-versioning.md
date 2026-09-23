# Bloem overlay: Release versioning

Bloem additions and overrides for the upstream Silo document [`docs/release-versioning.md`](../../release-versioning.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../README.md).

**Location:** section “Release versioning”, after the paragraph beginning “Silo uses GitHub Releases as the public record of shipped…”. **Bloem adds:**

## Bloem deployment records

The upstream release conventions below remain distinct from a Bloem operator
cutover. The [September 19, 2026 deployment](../../operations/2026-09-19-xtream-deployment.md)
used exact commit `418a18b7d`, not a new SemVer release or a claim about `latest`.
It records the approved CI-timeout exception and image/config-first rollback with
retained additive schema for that deployment. This does not establish general
downgrade compatibility or waive the green-check requirement for publishing a
release. Later documentation commits do not change the running application image.
