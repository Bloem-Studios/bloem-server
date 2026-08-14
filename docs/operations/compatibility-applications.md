# Compatibility Applications — Deployment and Operations

Vondel's Jellyfin and Audiobookshelf compatibility surfaces run as two
optional, removable companion applications:

- `vondel-audiobookshelf` — serves the Audiobookshelf protocol under
  `https://vondel.example/audiobookshelf`.
- `vondel-jellyfin` — serves the Jellyfin protocol on the canonical address
  plus `https://vondel.example/web`.

Both are private software. Their source repositories and images stay private
indefinitely; images are pulled from `ghcr.io/vondel-media` and require
authenticated access (`docker login ghcr.io` with a token that can read the
`vondel-media` packages). There is no public installation path.

Commands in this guide assume the repository root is the cwd.

## Security contract

A companion is a protocol translator, nothing more. Structurally enforced by
`scripts/verify-compat-compose.sh` (run `bash scripts/verify-compat-compose.sh`
any time; `scripts/verify-compat-compose_test.sh` proves the scanner detects
violations).

The scan is **default-deny in two dimensions**:

- **Service set.** A rendered combination may contain the base stack plus
  exactly the companions its overlays activate. An extra service — however
  innocuous it looks, and even if hidden behind a Compose profile — fails the
  scan.
- **Service keys.** A companion service may declare only these keys: `image`,
  `environment`, `networks`, `volumes`, `secrets`, `security_opt`, `ports`,
  `restart`. Each one also has its *values* checked. Every other key —
  `volumes_from`, `devices`, `privileged`, namespace sharing, `tmpfs`,
  `sysctls`, `runtime`, `healthcheck`, `labels`, and anything a future Compose
  release introduces — fails by construction.

Nothing is on those lists speculatively: a key qualifies only by being used by
a committed contract *and* having its values pinned. `healthcheck` and `labels`
are instructive exclusions — a healthcheck command runs arbitrary code inside
the companion (it can read the enrollment secret and post it to a service on
the shared network), and labels are inert only until a label-driven proxy on
the host acts on them and republishes an internal companion. If a companion
genuinely needs such a key, add it to the allowlist deliberately, with a value
check, under review — do not work around the scan.

**Scan reach.** The scan renders the committed files with the operator
environment excluded, so it verifies the two interpolated variables per
companion (`VONDEL_*_IMAGE` and `VONDEL_*_ENROLLMENT_FILE`) **only at their
committed defaults**. An operator who overrides them at deploy time — pointing
the enrollment secret at a different file, or the image at a different
registry — is outside what the scan can see. Treat those two variables as
security-relevant configuration and review them the way you review the compose
files themselves.

What that enforces:

- **No host ports.** Companions sit on the internal `vondel-compat` container
  network. All client traffic enters through Vondel's own listener, which
  forwards only a fixed, reviewed set of protocol paths. The diagnostics
  override below is the sole, loopback-only exception.
- **No Vondel credentials or data planes.** A companion never receives
  Vondel's database, Redis, media filesystem, Docker socket, `SECRET_KEY`,
  metadata-provider credentials, or tuner credentials. Its only Vondel contact
  is the private compatibility API (`/api/internal/compat/v1`) over the
  private network, authenticated with its own scoped service credential.
- **Secrets are files, never environment values.** The single-use enrollment
  token is mounted as a Docker secret at
  `/run/secrets/vondel_compat_enrollment`.
- **Disposable state only.** Each companion owns one named volume of protocol
  state (SQLite with WAL by default). Deleting it loses client-token
  correlation and presentation preferences; every canonical thing — progress,
  favorites, collections, playlists, downloads, identity — lives in Vondel.
  The state volume must be defined by the compose project itself — declaring
  it `external: true` or giving it an explicit `name:` (which could alias a
  pre-existing host volume) is rejected by the structural scan. Larger
  installs may instead point a companion at its *own* PostgreSQL database
  (never Vondel's server, database, schema, or credential); supply that DSN
  as a secret file, not an environment value — the scan rejects
  database-shaped environment keys and DSN-shaped values in any form.
- **Vondel never controls Docker.** The admin UI reports state and prints
  exact commands; a human or an external deployment controller runs them.

## Install and enroll (same host)

1. **Log in to the private registry** on the Docker host:

   ```bash
   docker login ghcr.io
   ```

2. **Create an enrollment token** in the Vondel admin UI: Admin →
   Compatibility Applications → Enroll. Tokens are single-use and expire after
   15 minutes; mint one per companion.

3. **Write the token to the secret file** the overlay mounts (the `.secrets/`
   directory is gitignored):

   ```bash
   mkdir -p .secrets/compat
   umask 177
   printf '%s' '<token>' > .secrets/compat/vondel-audiobookshelf-enrollment.token
   ```

   (For Jellyfin: `.secrets/compat/vondel-jellyfin-enrollment.token`. These
   paths can be moved with `VONDEL_AUDIOBOOKSHELF_ENROLLMENT_FILE` /
   `VONDEL_JELLYFIN_ENROLLMENT_FILE`, but prefer the defaults: whatever file
   these name is mounted into the companion at the enrollment path, so an
   override points a host file into the container and is not covered by the
   structural scan. Keep the default unless you have a specific reason, and
   review the value if you change it.)

4. **Activate the overlay.** This is the supported activation path — the
   companions are deliberately absent from the base `docker-compose.yml`:

   ```bash
   docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml up -d
   ```

   Jellyfin:

   ```bash
   docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml up -d
   ```

   Both (list every overlay in each subsequent `docker compose` invocation for
   this stack):

   ```bash
   docker compose \
     -f docker-compose.yml \
     -f docker-compose.audiobookshelf.yml \
     -f docker-compose.jellyfin.yml \
     up -d
   ```

5. **Readiness handshake.** On start the companion reads the token from
   `/run/secrets/vondel_compat_enrollment`, enrolls against the private API,
   negotiates its supported API range, and reports health. It does not become
   ready — and Vondel's gateway does not route to it — until Vondel has
   verified health and API compatibility **and** an administrator has enabled
   routing in Admin → Compatibility Applications. Starting a container never
   claims public routes by itself. An incompatible API range fails closed with
   an actionable error in the admin UI.

6. **Destroy the token.** The token is consumed on first use and worthless
   afterwards, but do not leave secret material on disk:

   ```bash
   rm .secrets/compat/vondel-audiobookshelf-enrollment.token
   ```

7. **Hand out the client address.** Clients always use the canonical Vondel
   address shown in the admin UI — never a container address:
   Audiobookshelf apps use `https://vondel.example/audiobookshelf`; Jellyfin
   apps use `https://vondel.example/`.

## Versions, pinning, update, rollback

Admin → Compatibility Applications shows each companion's reported version,
the **resolved image digest**, its API range, health, last contact, and active
sessions. Compare the digest there with what the registry serves when auditing
what actually runs.

The overlays default to the private `latest` tag. For deterministic change
control, pin via `.env` or the environment:

```bash
# Tag pin
VONDEL_AUDIOBOOKSHELF_IMAGE=ghcr.io/vondel-media/vondel-audiobookshelf:1.4.2
# Digest pin (strongest)
VONDEL_AUDIOBOOKSHELF_IMAGE=ghcr.io/vondel-media/vondel-audiobookshelf@sha256:<digest>
```

**Update** (the admin UI shows when an update is available and prints these
commands; Vondel itself never touches Docker):

```bash
docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml pull vondel-audiobookshelf
docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml up -d vondel-audiobookshelf
```

**Rollback:** set the image variable to the previous tag or digest (the admin
UI's version history lists what ran before), then run the same two commands.
The new container re-uses the existing service credential and state volume; no
re-enrollment is needed. If the rolled-back image's API range is incompatible,
it fails closed and only that companion's routes answer
`compatibility_unavailable` — native Vondel is unaffected.

## Disable, revoke, uninstall

- **Disable routing** (reversible, admin UI): the gateway stops forwarding the
  companion's paths, which answer with a protocol-appropriate
  `compatibility_unavailable`. The container may keep running; its volume and
  credential are preserved. Re-enable at any time.
- **Stop the container** (reversible): `docker compose ... stop
  vondel-audiobookshelf`. State volume preserved.
- **Rotate credentials** (admin UI): issues a fresh service credential and
  invalidates the old one. Restart the companion afterwards.
- **Revoke** (admin UI): immediately invalidates the companion's service
  credential and every compatibility session it created. A revoked companion
  must be re-enrolled with a fresh token to return.
- **Uninstall:** revoke in the admin UI, then remove the container and — if
  you also want the disposable protocol state gone — the named volume:

  ```bash
  docker compose -f docker-compose.yml -f docker-compose.audiobookshelf.yml down
  docker volume ls | grep vondel-audiobookshelf-state   # find the project-prefixed name
  docker volume rm <project>_vondel-audiobookshelf-state
  ```

  This loses only protocol state (clients log in again; Jellyfin display
  preferences reset). All canonical media and identity state remains in Vondel
  and reappears unchanged after a reinstall.

## Diagnostics (loopback ports)

`docker-compose.compat-diagnostics.yml` is the **only** sanctioned way to give
a companion a host port, and it binds `127.0.0.1` exclusively. Apply it last,
on top of **both** companion overlays (it names both services):

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.audiobookshelf.yml \
  -f docker-compose.jellyfin.yml \
  -f docker-compose.compat-diagnostics.yml \
  up -d
```

Running only one companion? Keep both overlays on the command line but name
the services you want started: `... up -d silo vondel-audiobookshelf`.

Defaults: Audiobookshelf on `127.0.0.1:13379`, Jellyfin on `127.0.0.1:8098`
(override with `VONDEL_AUDIOBOOKSHELF_DIAG_PORT` / `VONDEL_JELLYFIN_DIAG_PORT`).
Port publishing requires a non-internal network, so this override also
attaches the companions to the default bridge — remove the override as soon as
debugging ends by re-running `up -d --remove-orphans` without the diagnostics
file. Never port-forward or reverse-proxy these bindings; direct public
companion exposure is unsupported.

## Remote placement (mTLS)

Larger installations may run a companion on a different host. The contract
tightens rather than relaxes:

- **Mutual TLS is mandatory** in both directions between the remote companion
  and Vondel's private API. Server-only TLS is not accepted; each side
  presents a certificate the other verifies. Issue per-companion client
  certificates from an internal CA; never reuse Vondel's public certificate or
  its signing material.
- **Firewall allowlist:** the companion host accepts inbound traffic *only*
  from Vondel's egress address on the companion's listener port, and Vondel
  accepts companion traffic *only* on the private-API listener from the
  companion's address. The companion still gets no public exposure — client
  traffic continues to enter through Vondel's canonical address.
- **Enrollment** works the same way: mint a token in the admin UI, place it on
  the remote host as a root-owned `0600` file referenced by
  `VONDEL_COMPAT_ENROLLMENT_FILE`, transfer it out of band (not by email or
  chat), and delete it after the handshake.
- Point the companion at Vondel's mTLS endpoint with
  `VONDEL_COMPAT_API_BASE_URL=https://<vondel-internal-host>:<port>` and
  provide its keypair/CA as secret files. The exact certificate-file variable
  names ship with the companion images' own documentation.
- Credential rotation, revocation, health, and version display behave exactly
  as on the same host.

## Troubleshooting

| Admin state | Meaning | Operator action |
| --- | --- | --- |
| `unavailable` | Container absent, stopped, or unreachable on the private network | `docker compose ps`; check the companion overlay is in every compose invocation; `docker compose ... logs vondel-audiobookshelf` |
| `unhealthy` | Enrolled but failing health checks; gateway circuit-breaks its routes | Companion logs; native Vondel stays up — only the companion's paths answer `compatibility_unavailable` |
| `incompatible` | The image's API range does not overlap Vondel's; companion fails closed | Update the companion (or Vondel); pin a known-good tag/digest to roll back |
| `revoked` | Service credential invalidated by an administrator | Investigate first; re-enroll with a fresh token to restore |
| `disabled` | Administratively not routed | Enable routing in Admin → Compatibility Applications |
| Enrollment fails | Token expired (15 min), already used, or unreadable | Mint a fresh token; confirm the secret file exists and was written without a trailing shell prompt; check file permissions |
| Pull fails | Private registry authentication missing | `docker login ghcr.io` with a token that reads `vondel-media` packages |

Logs on both sides share a trace identifier per request and redact
credentials, cookies, tokens, and signed URLs; quote the trace ID when filing
an issue.
