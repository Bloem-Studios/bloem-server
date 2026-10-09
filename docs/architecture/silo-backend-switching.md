# Silo backend switching

A database can be served by the upstream Silo server or by Bloem, one at a
time, and the operator can switch between them in either direction without
losing or corrupting data. This is what lets a Silo installation try Bloem and
go back.

## Phases

The membership policy authority (`public.membership_policy_authority`) has
three phases:

| Phase | Policy authority | Who can run | Entered by |
|---|---|---|---|
| `compatibility` | frozen: no policy write succeeds on either binary | Silo and Bloem | the isolation migration |
| `mirrored` | `users` policy columns and the default-organization membership, kept identical | Silo or Bloem | `silo membership-policy enable-silo-switching` |
| `finalized` | `organization_memberships` only; the `users` columns are renamed `rollback_membership_*` | Bloem only | `silo membership-policy finalize` |

Allowed transitions are `compatibility → mirrored`, `compatibility →
finalized` and `mirrored → finalized`. Finalizing a mirrored database ends Silo
switching for good, so the CLI requires `--end-silo-switching`. There is no way
back to `compatibility`. Each phase change needs its own transaction-local
marker (`bloem.membership_policy_mirror_enabler`,
`bloem.membership_policy_finalizer`); the transition guard refuses anything
else.

## Invariants while mirrored

- **One organization.** Inserting a second organization, or a membership
  outside the default organization, raises
  `bloem_silo_switching_single_organization`. Silo has no organizations, so it
  cannot honor a second one.
- **Policy is identical on both sides at every commit.** A write to any of the
  13 policy columns on `users` (`users_policy_mirror_to_membership`) or on the
  default-organization membership
  (`organization_memberships_policy_mirror_to_user`) is copied to the other side
  in the same transaction. The transaction-local setting
  `bloem.membership_policy_mirroring = 'on'` stops the copy from bouncing back;
  both triggers restore it afterwards.
- **Bloem writers use `public.bloem_membership_policy_writer_marker()`.** It
  returns `'v1'` in `mirrored` and `finalized` and `''` otherwise. Never write an
  inline phase check in a policy writer.
- **No direct profile logins.** A profile with its own `login_email` or
  `password_hash` raises `bloem_silo_switching_direct_profile_login`. Silo would
  treat such a session as the whole account.
- **Silo-shaped writes are placed in the default organization.** A profile
  inserted without an organization gets the default organization, the account's
  membership (created if missing, its policy seeded from `users`) and that
  membership's access group (`user_profiles_00_silo_tenancy`). An account still
  without a membership at commit gets one (`users_silo_default_membership`, a
  deferred trigger that exists only while mirrored). A Bloem transaction that
  inserts an account and then alters the `users` table fails while mirrored,
  because PostgreSQL refuses `ALTER TABLE` on a table with pending trigger
  events.
- **`auth_sessions.device_id` accepts NULL as `''`** in every phase. Upstream
  writes NULL when a client sends no device headers.
- **Heartbeats.** Silo nodes register as legacy nodes, as in `compatibility`.

## What Silo does not see

While Silo serves the database, Bloem-only features do nothing: organizations
beyond the default one, entitlement templates, profile logins, Live TV and DVR,
native storage and presentation plugins. Their rows stay in the database and
work again when Bloem serves it.

## Operating rules

- **One backend at a time.** Stop one before starting the other. The policy
  mirror keeps data consistent; running both at once is still not supported
  (background jobs, caches and scheduled work assume one owner).
- **Separate caches.** Each backend has its own Redis, flushed before it starts.
- **Silo version pin.** Run a Silo image whose source revision Bloem has
  already merged (an ancestor of the deployed Bloem commit). A newer upstream
  can apply database changes Bloem has not adapted to.
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
`internal/tenancy/membership_policy_mirror_test.go` and
`internal/auth/bloem_repository_mirrored_test.go`.
