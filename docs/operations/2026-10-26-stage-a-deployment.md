# Bloem Server deployment — October 26, 2026 (Stage A)

This receipt records the second deployment on October 26: `main` with the
Stage A simplifications. Production had run `d183b286f` since 00:05 UTC.

## Deployed source and artifacts

| Item | Verified value |
| --- | --- |
| Application source on published `main` | `7831034eaca80b1d16f8cebcb1852feb9a8c2fe1` |
| System-info server version | `7831034e` |
| Previous application source | `d183b286f` |
| Runtime image | `bloem-server:release-7831034ea` |
| Server binary SHA-256 | `fbe3e2e01f3806bf227ec54c622334dce4d973e7f6566a00df087c90078db07d` |
| Presentation workers | none; promotions and ambience evaluate in-process |
| Migrations applied in this deployment | 3, all Bloem's and forward-only; zero pending |
| Applied migration count | 722 |
| Maximum migration version | `20261026030000` |

The three migrations:

- `20261026003000_bloem_account_policy_on_users`: account policy back on `users`, memberships become
  link-only with `UNIQUE (account_id)`, and the mirrored phase becomes
  `bloem_silo_switching` enabled;
- `20261026020000_bloem_remove_music`: drops the empty music tables;
- `20261026030000_bloem_remove_lifecycle_receipts`: drops the four receipt tables, which held 129
  historical receipts.

## What changed

The source includes:

- A2: account policy on `users` and Silo switching as one flag;
- A9: music removed;
- A5: presentation evaluators in-process;
- A7: dead code removed;
- A6: Silo's root handler on the public port, the default-bundle grant for new
  organizations, and the series-mute fix in the tenancy notification fanout;
- A8: Bloem CI in its own workflow;
- A1: lifecycle receipts removed;
- C16: downloads read the profile from context;
- C12 and C14: Bloem's scenario rows and bundle budget out of Silo's files;
- C7: web organization admin closer to Silo;
- the C5 trim.

The seam ledger went from 344 to 309. See the [changelog](../bloem/CHANGELOG.md).

## Build

The image was built locally from a `git archive` of the source, with the web
bundle injected as the `frontend_dist` build context, as on 00:05 UTC. CI could
not publish it: Silo's `TestLogPartitionsMatchPartmanOnNonUTCServer` fails on
every branch until its 8-day window passes the 2026-11-01 US daylight-saving
change. Locally, before the push, these passed:

- the full Go suite with the test database (230 packages);
- the race runs for tenancy, resourcetenancy, access, policy, auth and queryattr;
- the scenario executor;
- every verify target;
- the changed-line lint;
- web typecheck, lint, build and the bundle check.

In the web suite, three timing-sensitive vitest files failed under host load and
passed when run alone.

## Procedure and result

Before the restart:

- **Read-only production checks:**
  - phase `mirrored`;
  - 128 accounts, each with exactly one membership, and no policy divergence
    between users and memberships;
  - no access group outside its organization;
  - no profile logins;
  - no music rows.
- **Rehearsal:** the three migrations ran on a restored production schema with
  synthetic rows.
- **Backups:**
  - container snapshot `pre-main-7831034ea`;
  - database restore point `pre-main-7831034ea` (LSN `3B2/EE1605F8`);
  - dumps of the configuration tables and of every table the migrations change
    or drop (users, memberships, policy authority and snapshots, access groups,
    profiles, heartbeats, receipts, music, home dismissals);
  - WAL archiving current.
- **Policy checksum:** an MD5 over every account's 13 policy columns,
  `917ce605e96ee17f5b7d66408c6cde18`.

The restart script refused to run unless the live image was `d183b286f`. It
stopped the server in 38 seconds, ran the migrations offline in 5 seconds, and
started the new image. The server was ready 49 seconds after the stop began,
healthy with no restarts. The runtime fingerprint, covering configuration and
mounts, did not change.

After start:

- ready and health returned 200 locally and publicly;
- system info reports `7831034e`;
- zero pending migrations;
- `bloem_silo_switching_enabled()` is true;
- `organization_memberships` has 8 columns and the one-per-account constraint;
- 128 accounts and 128 memberships; 164 profiles;
- the policy checksum is unchanged;
- no membership-policy, music or lifecycle-receipt table remains;
- an active stream resumed;
- no ERROR or panic lines in the first minutes; the only warnings were
  cold-cache "slow section fetch" lines.

## Rollback

All three migrations are forward-only. To go back, restore the database to the
restore point above, with the container snapshot, rather than starting the old
image on the new schema.

## Follow-up deployment: account provisioning (06:41 UTC)

`main` at `d064644a8b`, image `bloem-server:release-d064644a8`, server binary
SHA-256 `976593ba6104b7cf68bfa6fa1ba052de750c441662bfae2f68d3d097600af8c5`.

What it changes:

- Account creation gives an account its one membership in the account
  repository, so the `MembershipProvisioner` seam goes.
- Tenant accounts are created directly in their tenant.
- An account's default profile lands in the organization it is created in.
- The seam ledger goes from 309 to 306.

There were no migrations. Before the restart:

- container snapshot `pre-main-d064644a8`;
- restore point `pre-main-d064644a8`, LSN `3B4/98A6A558`.

The server stopped in 32 seconds and was ready 42 seconds after the stop began,
healthy with no restarts. The runtime fingerprint did not change. Afterwards:

- system info reports `d064644a`;
- ready and the public site returned 200;
- 722 applied migrations, max `20261026030000`;
- Silo switching is enabled;
- 128 accounts and 128 memberships;
- the policy checksum is still `917ce605e96ee17f5b7d66408c6cde18`;
- an active stream continued;
- no ERROR or panic lines.
