# Silo backend switching

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current. Silo switching is a Bloem addition: Silo knows nothing about it, and
> its job is to keep a database that Bloem serves readable and writable by an
> unmodified Silo image. It was introduced on 2026-10-09 (`474782ef1` for this
> page; migrations `20261009131027_bloem_membership_policy_mirrored.sql` and
> `20261009131835_bloem_silo_switching_writes.sql`; `ac79641f0` for profiles,
> roles and ungrouped admins). `fc688aca3` (2026-10-13) kept Silo's compatibility
> data, and `c84b4f3f8` (2026-10-14) restored the v1 health identity. The
> current single-flag design comes from `c60b7008e` (2026-10-26, migration
> `20261026003000_bloem_account_policy_on_users.sql`), which moved account policy
> back onto `users` and removed the earlier three-phase membership-policy
> rollout. `2393c41ee` (2026-10-18) restored Silo's Jellyfin and Audiobookshelf
> servers in Bloem; the section on what each backend sees was updated for that
> on 2026-10-27.

A database can be served by the upstream Silo server or by Bloem, one at a
time, and the operator can switch between them in either direction without
losing or corrupting data. This is what lets a Silo installation try Bloem and
go back.

## The switch

Account policy lives on `users`, where Silo keeps it, so both servers read and
write the same columns. What remains is what Silo cannot honor, and one flag,
`public.bloem_silo_switching`, keeps the database inside it:

| Command | Effect |
|---|---|
| `silo silo-switching status` | Reports whether the database is switchable. |
| `silo silo-switching enable` | Refuses unless there is exactly one organization and no profile has its own login. Then gives every account its default-organization membership, reconciles membership roles from `users.role`, and sets the flag. Idempotent. |
| `silo silo-switching disable` | Clears the flag, lifting the guards below. Silo must not serve the database afterwards. |

`public.bloem_silo_switching_enabled()` reads the flag; every guard and
placement trigger below checks it. A database that was in the `mirrored`
membership-policy phase before `20261026003000_bloem_account_policy_on_users`
kept switching enabled through that migration.

## Invariants while switchable

- **One organization.** Inserting a second organization, or a membership
  outside the default organization, raises
  `bloem_silo_switching_single_organization`. Silo has no organizations, so it
  cannot honor a second one.
- **Group moves carry profiles; roles follow both ways.** Bloem applies a
  profile's own access group, so when Silo moves an account to another group,
  the account's profiles that were on the old group move with it
  (`users_silo_profiles_follow_group`, which also lets Silo delete a group
  after moving its members). `users.role` and the membership's `legacy_role`
  are kept in step (admin or user; other account roles are ordinary members).
  An ungrouped admin stays ungrouped.
- **No direct profile logins.** A profile with its own `login_email` or
  `password_hash` raises `bloem_silo_switching_direct_profile_login`. Silo would
  treat such a session as the whole account.
- **Silo-shaped writes are placed in the default organization.** A profile
  inserted without an organization gets the default organization, the account's
  membership (created if missing) and the account's access group
  (`user_profiles_00_silo_tenancy`). An account still without a membership at
  commit gets one (`users_silo_default_membership`, a deferred trigger that
  exists only while switchable). A Bloem transaction that inserts an account
  and then alters the `users` table fails while switchable, because PostgreSQL
  refuses `ALTER TABLE` on a table with pending trigger events.
- **`auth_sessions.device_id` accepts NULL as `''`.** Upstream writes NULL when
  a client sends no device headers.

## What Silo does not see

While Silo serves the database, Bloem-only features do nothing: organizations
beyond the default one, entitlement templates, profile logins, Live TV and DVR,
native storage, promotions and ambience. Their rows stay in the database and
work again when Bloem serves it.

Silo's Jellyfin and Audiobookshelf compatibility servers are shared, not
Bloem-only. Bloem removed both protocols on 2026-10-13 and kept their tables,
the author-count view and their settings for Silo (`fc688aca3`). Since
`2393c41ee` (2026-10-18) Bloem runs both servers again as Silo ships them, so
whichever backend is serving reads and writes those tables. The Jellyfin
setting `jellyfin_compat.server_id` (the server identity that Jellyfin clients
recognise) also supplies `server_id` on `GET /api/v1/health` in both backends,
as in Silo. See [protocol compatibility](bloem-compatibility-removal.md).

## Operating rules

- **One backend at a time.** Stop one before starting the other. Running both
  at once is not supported (background jobs, caches and scheduled work assume
  one owner).
- **Separate caches.** Each backend has its own Redis, flushed before it starts.
- **Silo version pin.** Run a Silo image whose source revision Bloem has
  already merged (an ancestor of the deployed Bloem commit) and that has every
  Silo migration Bloem has applied to the database. A newer upstream can apply
  database changes Bloem has not adapted to; an older one can expect a schema
  that a later Silo migration changed. For example,
  `20261019181724_hash_api_key_verifiers.sql` replaces the raw `api_keys.api_key`
  column with sha256 verifiers in `key_hash`, which a Silo image from before
  that migration cannot read.
- **Rollback point.** Snapshot the database before the first switch.

## Checking a change

`make silo-switch-check SILO_IMAGE=<silo image by digest> BLOEM_IMAGE=<tag>`
runs both images against one database:

1. Silo installs and creates accounts.
2. Bloem migrates the database and enables switching.
3. Bloem, Silo and Bloem again each read what the previous backend wrote and
   write something the next one must read.

Run it whenever a change touches a table that both servers write. Run it too
after merging upstream changes that add Silo writes to tables Bloem constrains.
The database-level tests are in `internal/tenancy/silo_switching_test.go`,
`internal/tenancy/silo_switching_writes_test.go`,
`internal/auth/bloem_repository_switching_test.go` and
`migrations/bloem_account_policy_on_users_test.go`.
