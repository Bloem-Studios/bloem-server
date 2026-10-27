# Bloem Server deployment — October 15, 2026

> Historical record, true to October 15, 2026; later deployments superseded it. The newest deployment receipt is the [October 27, 2026 receipt](2026-10-27-main-deployment.md).

This receipt records deployment of the published browse-only preview server
candidate. The software is installed; public preview remains disabled and has
no assigned preview account. Site publishing, preview routing, library selection
and plugin upgrades remain separate rollout work.

## Deployed source and artifacts

| Item | Verified value |
| --- | --- |
| Application source on published `main` | `21fae6837267fe95ef8fc79e1ee6bb5a1f1346d2` |
| System-info server version | `21fae683` |
| Previous application source | `c3523bb5f` |
| Final runtime image | `bloem-server:release-21fae6837-presentation1` |
| Final runtime image ID | `sha256:c73b37b72ef8361fe8466cb840329a192e0a438fce065f1663f37021800a896e` |
| Server binary SHA-256 | `3d3d087f9311f2df36d845c5b072309450d818652ed16cfe6890ad5253a2b945` |
| Promotions worker SHA-256 | `89179798fac9e1a4ffc7d3744e2e791c26eb545f2077474c428cc0612a96ab28` |
| Ambience worker SHA-256 | `7b10fb0e7f1734a73f63c9ef683d297add591f57368bd9f2536ed20d88bfbc38` |
| Applied migration count | 674; zero pending |
| Maximum migration version | `20261014233826` |
| Served v2 OpenAPI paths | 691 |
| Served OpenAPI SHA-256 | `bbd3432c9dff212cf406bb7a0c890805600f3baa2bf7a84aa5adf34569503301` |

Documentation follow-up commits do not change these deployed executables. The
source publication and this installation do not establish mobile distribution
or native-client adoption of the current contracts. See the
[contract transition](../architecture/bloem-contract-adjudications.md) and
[SDK compatibility guide](../architecture/bloem-sdk-compatibility.md).

## Offline migration and configuration preservation

The integrated application stopped cleanly with exit code zero and no OOM.
There were no other clients connected to its database before the application
migration runner applied all 24 pending migrations, including registered Go
migrations, in approximately nine seconds. The subsequent status audit found
674 applied migrations and none pending. Raw SQL and manual ledger changes were
not used.

The deployment retained the existing Compose project and every override, adding
an image-selection override last. Before/after fingerprints matched for the
environment, mounts, published ports, entrypoint, command, runtime user, working
directory, restart policy, network membership and security settings. PostgreSQL
and Redis remained available.

The operator explicitly waived the redundant database dump in favor of existing
point-in-time recovery (PITR). PITR coverage and restore were not independently
tested during this deployment. The interrupted dump is incomplete and is not a
valid recovery artifact. This rollout-specific decision does not replace the
general [backup guidance](../bloem/overlays/wiki/deployment/docker.md#backups-and-updates).
Do not select an older application image as an automatic schema rollback;
review the actual migrations and recovery state first.

## Presentation-worker correction

The initial candidate replaced the server executable while inheriting older
bundled presentation workers. Both workers rejected the server's
`bloem.presentation.ready` operation, producing two startup warnings even
though application health passed. Running a worker with closed stdin had not
exercised this exchange.

Both workers were rebuilt from the exact deployed application source and
packaged into the final runtime image. Each candidate worker passed the
newline-delimited version-one readiness exchange with payload `true`, using
only `LANG=C.UTF-8`, `LC_ALL=C.UTF-8` and `TZ=UTC` in its environment. These
checks ran locally and in the candidate image before installation. The server
executable and preserved runtime configuration were unchanged by this packaging
correction. Independent verification at 05:14 UTC found both host-managed worker
processes running and no presentation startup warnings.

For future custom runtime images, build and verify all bundled executables
together. An application health response or successful closed-stdin exit is
insufficient evidence of worker protocol compatibility. The repository
Dockerfile builds both workers alongside the server.

## Verification and activation boundary

Independent checks confirmed:

- Application health and readiness returned HTTP 200 directly and through the
  public HTTPS route; the container was healthy with zero restarts.
- The running server binary matched the candidate hash and system info reported
  the expected revision. Root HTML and all four entrypoint assets loaded on both
  origins.
- The served OpenAPI document exactly matched the published source contract.
  Swagger HTML, initializer, JavaScript bundle and stylesheet loaded.
- Unauthenticated preview administration and v2 user administration returned
  HTTP 401; an empty preview launch request returned HTTP 400.
- An existing authenticated web session retained administrative navigation after
  reloading the deployed application.
- The protected preview entitlement template was seeded. Preview was disabled,
  no preview account was assigned, and there were no guest preview sessions.

The deployment checks do not certify production native playback, pause/resume,
all providers, all devices, a long soak, or a recovery drill. Structured logs after
the worker correction contained one error-level planned-playback request with
cause `context canceled`; that request was not replayed or accepted as a playback
test. Broad text searches also matched error text embedded in lower-severity
FFmpeg messages, so their counts do not represent host error counts.

Background thumbnail, metadata/rescan and NFO warnings require their own
content-specific investigation; PostgreSQL also reported that some tuning
settings need a restart. No PostgreSQL restart was performed as part of this
application deployment.

The [browse-only policy](../architecture/bloem-browse-only-content-policy.md)
documents preview principals, guest sessions and content-delivery restrictions.
Public preview activation still requires the operator's routing, account and
library configuration plus end-to-end acceptance.
