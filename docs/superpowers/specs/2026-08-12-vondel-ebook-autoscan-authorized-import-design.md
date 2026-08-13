# Vondel Ebook and Autoscan Authorized Import Design

## Decision and authorization

The project owner states that they control the licensing rights required to
copy, modify, and license the Ebook Metadata and Autoscan ARR upstream source.
The owner directs Vondel to import both codebases, license the Vondel copies
under **GNU Affero General Public License v3.0 or later
(`AGPL-3.0-or-later`)**, and keep both Vondel repositories permanently private.

This decision replaces the permission-gate treatment recorded during the
initial source-fork phase. The existing four-file gate roots contain no
upstream source and may be advanced normally; no history rewrite is required.

## Exact upstream snapshots

- Ebook Metadata: `Silo-Server/silo-plugin-metadata-ebook` tag `v0.1.1`, commit
  `70d8acb4dbb68f8cc594099258211cd9a3a3082f`.
- Autoscan ARR: `Silo-Server/silo-plugin-autoscan-arr` tag `v0.1.2`, commit
  `7987ddae852549f5f2ef4e00b6f25dfa5497ddad`.

The snapshots are imported with `git archive`; upstream Git history and tags
are not added to the Vondel repositories. The existing Vondel gate commits
remain the zero-parent roots and the authorized imports are normal descendant
commits.

## Licensing and provenance

Before source is committed, each repository receives the complete
AGPL-3.0-or-later license text and an updated `UPSTREAM.md`/`NOTICE` recording:

- exact upstream repository, tag, and commit;
- the owner's authorization to create and license the Vondel copy;
- material Vondel changes;
- Vondel's independence and lack of upstream affiliation or endorsement.

Existing upstream copyright notices remain intact. Vondel does not claim that
the upstream repositories themselves were previously offered under AGPL.

## Compatibility and identity

The imports preserve the existing compatibility contracts:

- Ebook plugin ID `silo.ebook-metadata`;
- Ebook capability `metadata_provider.v1/ebook-metadata` and priority `ebook: 2`;
- Ebook configuration keys `enabled_sources`, `google_books_api_key`,
  `isbndb_api_key`, `hardcover_api_key`, and `default_region`;
- Autoscan plugin ID `silo.autoscan.arr`;
- Autoscan capability `scan_source.v1/arr`;
- `silo_api_version: v1`, protobuf namespace `silo.plugin.v1`, and all existing
  request/configuration semantics.

Repository, module, presentation, publisher, documentation, support, and
source URLs become Vondel identities. Both Go modules pin
`github.com/Vondel-Media/vondel-plugin-sdk v0.13.3` without a `replace`
directive.

## Private automation and releases

Both repositories use the same trusted private-SDK CI boundary as the reviewed
Vondel plugins:

1. `pull_request_target` runs the trusted base workflow with minimal read
   permissions.
2. A checkout-free job downloads only SDK `v0.13.3` using a scoped read token,
   removes authentication, and publishes a sanitized short-lived module-cache
   artifact.
3. Separate secretless jobs explicitly check out the PR-head or push SHA and
   run repository-controlled tests/builds.

Release workflows are pushed-tag-only, require strict stable SemVer, exact
manifest/tag equality, an annotated tag resolving to reviewed `origin/main`,
and private repository visibility. Releases contain exactly three platform
binaries plus `checksums.txt`. No workflow may change visibility, publish to a
public registry, or dispatch to a Silo repository.

First Vondel release versions are patch bumps:

- Ebook Metadata `v0.1.2`;
- Autoscan ARR `v0.1.3`.

These releases and repositories remain private indefinitely. They are included
in the authenticated materialization, token-free staging catalog, and
acceptance suite, but never in a public catalog or publication operation.

## Testing

Each authorized import must pass its complete upstream test suite after module
rewrites, plus race, vet, tidy-diff, three-platform builds, identity tests,
license/provenance tests, private-workflow security tests, release guard tests,
and secret/publication scans.

Ebook acceptance verifies metadata matching and configured provider-secret
redaction using deterministic fixture HTTP services. Autoscan acceptance
verifies Sonarr/Radarr change polling, path handling, deduplication, and the
`scan_source.v1` response without logging connection credentials. The server
continues to own validation, rewrites, suppression, and scan enqueueing.

## Completion criteria

This design is complete when both private repositories contain the authorized
source and AGPL-3.0-or-later license, pass review and CI, publish verified
private releases, appear in the private Vondel catalog/materialized staging
tree, and pass their deterministic acceptance tests. Public release is
permanently out of scope for these two repositories.
