# Bloem deployment — October 30, 2026: upgrade prompt and new arrivals

Production runs `main` at `b99aa105c6352c0f7144c8a3dbd6ad22f268a8ef` from 19:49 UTC. The
owner asked for both changes to be deployed without waiting for CI. They serve a
storefront's sales tools, and both stay inert until a storefront turns them on.

## What changed

- **Upgrade prompt** (`c0d629550`). When a stream is refused because the account's plan
  is already playing on as many screens as it allows, the server can put an alert with
  a button into the viewer's inbox and send it as a push where the profile allows that.
  A profile gets it at most once a day, and the alert expires after three days. A
  platform administrator reads and replaces the wording and the link with `GET` and
  `PUT /api/bloem/v1/admin/platform/upgrade-prompt`. The prompt is off until it is
  saved with `enabled: true`. Migration `20261030162617_bloem_upgrade_prompt` adds the
  one-row table that holds it. The seam ledger is unchanged.
- **New arrivals** (`b99aa105c`). `GET /api/bloem/v1/admin/platform/new-arrivals` lists
  films and series with episodes first seen in enabled libraries in the last 1 to 31
  days, for a storefront's weekly email. It returns titles, years and counts only. No
  migration.

See the [Bloem API reference](../bloem-api-reference.md) for both contracts.

## Procedure and result

- **Image.** Built locally from a `git archive` of the commit, with the Bloem Image
  workflow's build arguments and OCI labels. The web bundle was built from the same
  commit. Tagged `bloem-server:release-b99aa105c` and copied with `docker save | docker
  load` to CT135 and to the transcode node CT172. The binary's SHA-256 is
  `431416df931bb91e0d4dee0ac8465ca7655e3bedbe4eb7b1ebc8a1427d2d7662`, the same locally
  and in the running container.
- **Before the restart:** container snapshot `pre-main-b99aa105c`; database restore point
  of the same name at LSN `3EE/77B489E8`. Segment `00000003000003EE00000077`, which holds
  it, was archived before the stop, with no archive failures.
- **Restart.** The restart script refused to run unless the live image was `8f4ba105c`.
  One transcode was running. The stop began at 19:47:43 and reached the 120-second
  grace limit, so Docker killed the old server (exit 137, not out of memory). The one
  migration applied in about a second, and 740 are applied with none pending. The
  server answered ready at 19:49:52, 129 seconds after the stop began, and was healthy
  with no restarts. Before the next deploy, check for running transcodes: a stream
  holding the server open is what stretches the stop to the grace limit.
- The runtime fingerprint is unchanged apart from the image. `/api/v2/system/info`
  reports `b99aa105`. Both new routes answer 401 without a credential. No error lines
  were logged after the start, and a transcode was running again afterwards.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) were idle and
  now run `release-b99aa105c`, healthy. Their previous compose files are kept with
  suffix `.bak-pre-b99aa105c`.

Neither the prompt nor the list was exercised against production data in this
deployment: the prompt is disabled until a storefront saves one.

## CI

Bloem CI on `b99aa105c` finished after the deploy with one failure:
`TestAllowlistMatchesTree` in shard 4/4 found the three new wire types neither
registered nor given a reason in the client coverage allowlist. Every other job passed.
`23e25924f` adds the three reasons to `contracts/client/v1/coverage.json`. That file is
read only by the client generator and its tests, so the deployed binary is unchanged.

To roll back, start `bloem-server:release-8f4ba105c` again. The migration only adds a
table that image never reads; the previous image was not started against the new schema
to check. Restoring the database to the restore point together with the container
snapshot also rolls back, but discards everything written since 19:47.
