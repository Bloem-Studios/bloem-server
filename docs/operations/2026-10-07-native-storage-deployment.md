# Bloem native storage deployment — October 7, 2026

> **Frozen snapshot.** This receipt records the October 7 deployment of the
> native onboarding implementation, which was later replaced by storage-source
> library locations. Its facts are historical; for the current design see the
> [storage architecture](../architecture/bloem-native-storage.md).

This is the deployment receipt for the native EPUB/PDF onboarding implementation
and the next 27 Silo commits. It records observed checks, not a claim that every
client, provider or historical test suite has passed.

## Source and deployed revision

| Item | Verified value |
| --- | --- |
| Bloem application deployed | `c87b44545228c93f909275c0b5c4fc1d6a289e1b` |
| Included Silo revision | `74158b4a8a799192312c13253552b8030af8575c` |
| Previous included Silo revision | `1555e5214177453d89e698c2b362fa92c5bd96f8` |
| Imported upstream commits | 27; every commit is an ancestor of the deployed revision |
| Published source branch | `codex/native-storage-20261006` |
| Native onboarding implementation | `b62833a1430d1fd499b57d188e05c716dd0f4105` |
| Normal upstream merge | `b5b9202802ca1bf66d0251ad0b82fd272d1992ba` |
| Local deployment image | `bloem-server:native-onboarding-c87b44545` |
| Container platform | Linux/amd64 |
| Binary SHA-256 | `ea59b303b20470a637077db48a284bd73c9266ec0ab30191ef1c817db1571f4d` |
| Declared upstream-file seams | 407 |
| Running system-info version | `c87b4454` |
| Deployment verification | October 7, 2026, 11:16 UTC |

The source branch was pushed to Bloem's repository. This did not merge the branch
into `main`, publish a new GHCR image tag, or create a Silo release. The local image
was built from the clean commit. Source and loaded images had identical rootfs
layers and binary hashes; Docker versions produced different image IDs, so an
image-ID comparison alone was not used as artifact proof. Later documentation
and SDK commits do not change this deployed binary.

## Included changes

Native storage now composes protected source installation/configuration,
pathless ebook library creation and recovery, initialization, binding, full-scan
queueing, authorized EPUB/PDF publication and reader delivery. Disable and
uninstall retain identities and reading progress while fencing further access.
Capabilities report actual composition; `backend_verified` remains false.
The onboarding contract it followed has since been removed; see the
[storage architecture](../architecture/bloem-native-storage.md).

The upstream merge adds library/series/season/collection shuffle and episode
poster fields in download manifests, improves download admission and autoscan
polling/delivery, makes overlapping scans wait, and fixes profile-visible catalog
and person queries. Authentication retains local passwords when connecting
Tailscale and returns retryable 503 responses for session-store outages. Bloem
keeps its canonical membership/profile authority, native overlap cancellation,
and private storage authorization. Upstream CI/build and dependency updates are
included with their original ancestry.

## Migration and deployment boundary

The native onboarding migration requires an offline upgrade: drain every API,
worker, autoscan, administrator and direct scanner on every node. Empty job or
lease tables are not proof that writers stopped. Use the Bloem application
migration runner with its finalized-membership adapter and out-of-order migration
support; do not apply raw upstream Goose SQL or alter the version ledger.

This deployment stopped the single integrated application, verified an exit code
of zero with no OOM, no remaining application process, and no other database client
backends before selecting the new image. PostgreSQL and Redis were retained.
Only the application image setting changed; the existing Compose project, overrides
and other configuration were preserved.

The first cutover check stopped after two task execution-history inserts reported
`context canceled` during shutdown. Source review established that both inserts
occurred after their tasks returned, using the canceled application context.
The failed check remains recorded. Deployment resumed only after verifying the
exact two messages, unchanged stopped-container/configuration identity, and zero
remaining database clients. Other shutdown errors remained blocking.

**No database backup was taken, at the operator's explicit direction for this
rollout.** This is a record of that decision, not a general backup-policy change.
No database restore, Down migration or automatic old-image restart occurred.
Native Down migrations refuse retained state; choosing an older binary is not
proof of schema compatibility. Any recovery requires reviewing the actual applied
migrations and retained source/catalog state.

Startup reported migration completion. The application became healthy with zero
restarts. Independent checks confirmed health/readiness returned 200, system info
reported the expected revision, the running binary hash matched the transferred
artifact, and startup logs contained no error-level messages. Web entrypoint assets
loaded, and native administrative capability routes rejected absent and invalid
credentials. No authenticated production provider was installed by these probes.

## Verification before deployment

- Clean Go build, embedded-web build and 330 tests across 32 changed web test files passed.
- The broad selected Go command recorded 625 top-level passes and 39 skips, but
  its whole-command result remained failed because it began before a duplicate
  authentication `TestMain` was corrected. The corrected auth rerun passed all
  eight selected tests; the earlier command was not relabeled as passing.
- Real disposable-database regressions passed 45 selected auth, scanqueue,
  catalog and shuffle tests without skips or races. Owned clones were cleaned.
- Actual native finite HTTP authorization, capability composition, and real
  queue-worker gates each passed on separate guarded database clones. A final
  capability DB check after cleanup also passed.
- The cleanup-focused unit selection passed 41 tests without failures, skips or
  races. Changed-line lint, with issue caps disabled, completed with zero issues
  and no source drift. Failed and interrupted prior lint commands remain failed.
- Migration validation, portable-path checks, whitespace checks and the 407-path
  seam ledger passed. The final cleanup did not change that ledger.

These runs have different scopes; their counts are not one whole-suite result.

## Outstanding validation

Deployment health and synthetic executable fixtures do not certify a production
storage backend, provider release, process-restart reader continuity, all-node
revocation, native-device UX, or every existing application workflow. Unsupported
native repair, delete, unbind, conversion, downloads and populated-namespace
reinstallation remain unsupported. A passing SDK/schema check does not turn those
capabilities on.

The [SDK compatibility guide](../architecture/bloem-sdk-compatibility.md) records
source and generated-binding currency separately from client feature adoption.
Historical September and October 5 test failures and browser acceptance remain
attached to their dated records; this deployment does not retroactively resolve
them. Authenticated production onboarding and real provider/device acceptance
still require their own attributable results.
