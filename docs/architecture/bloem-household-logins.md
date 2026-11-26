# How household logins work

A household login is an account of its own that belongs to another account,
the household's **main account**. It signs in with its own username and
password, so the account holder can give someone a login without handing over
their own. A storefront creates logins and decides how the household's streams
and transcodes are split between the main account and its logins; the server
keeps the logins tied to the main account.

Servers that support household logins advertise the feature token
`household_logins_v1` on `GET /api/bloem/v2/capabilities`, and servers that
can also give a household a shared pool advertise `household_pool_v1`.

## What a login is

A login is a `users` row whose `household_main_user_id` names its main
account. It has:

- its own username, password, devices, sign-in sessions and profiles (at least
  one; it starts with one);
- the main account's access group and organization, so it sees the same
  libraries, with its own limits (`max_streams`, `max_transcodes`,
  `transcode_allowed`, `max_profiles`) on top. An account's own stream and
  transcode limits replace the group's, and for profiles the stricter of the
  account's and a managed group's applies, so a login can be held below the
  plan;
- no email address unless one is given; without one, its password is reset by
  an administrator.

A login cannot have logins of its own, and an admin account cannot have
logins.

## Invariants

- **A login is enabled only while its main account is.** The database keeps
  them in step, so the rule holds whichever server writes the account,
  Bloem or [stock Silo](silo-backend-switching.md)
  (`users_household_follow_main_enabled`, migration
  `20261122213808_bloem_household_logins_under_silo_switching`). Disabling a
  main account, in the same statement, disables each of its enabled logins,
  marks it `household_disabled`, bumps its access policy revision and revokes
  its sign-ins in the tables Silo checks (login, Jellyfin-compatible and
  Audiobookshelf-compatible sessions, approved device sign-ins:
  `bloem_household_revoke_sign_ins`, the statements of Silo's own
  `revokeSignInsInTransaction`). The account service then ends the logins'
  live connections as it does the main account's. Enabling the main account
  enables only the logins marked `household_disabled`. Because Silo checks
  `users.enabled` wherever it admits an account, this one rule covers sign-in,
  refresh, device sign-in and the compatibility servers alike.
- **A login cannot be enabled while its main account is disabled.** Bloem's
  account update refuses it first (`ErrHouseholdMainDisabled`), and the
  database refuses any other writer (`bloem_household_main_disabled`,
  `users_household_login_enabled_written`). Any other write of a login's
  enabled state is an administrator's and clears `household_disabled`, so a
  login an administrator turned off stays off when the main account comes
  back.
- **A login created under a disabled main account** starts disabled and
  `household_disabled`, and comes back with the main account.
- **A login follows its main account's group.** A plan change moves the main
  account by writing `users.access_group_id` directly, so the trigger
  `users_household_logins_follow_group` carries every such move to the logins
  and to their profiles that sat on the old group.
- **A login is deleted with its main account.** The account service
  (`DeleteAdminAccount`) deletes a main account's logins first, each through
  the same path so each gets a deleted account's cleanup, then the main
  account. The foreign key is `ON DELETE RESTRICT`, so a plain `DELETE` of a
  main account that still has logins is refused rather than leaving logins
  behind, except while Silo switching is on: then stock Silo's one-statement
  delete takes the logins with it (`users_household_delete_with_main`). A
  conditional delete checks the main account's revision before any login is
  deleted.

## API

Under the platform credential, beside the other platform account routes:

- `POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins` creates a
  login: `username`, `password`, `profile_name`, `max_streams` (at least 1),
  `max_profiles` (at least 1), and optionally `max_transcodes`,
  `transcode_allowed`, `email` and `require_password_change`. The password
  must meet the server's password policy, as any password an administrator
  sets does. It answers the account as the account service reads it, with
  `household_main_user_id` and `household_disabled`.
- `GET /api/bloem/v2/admin/platform/accounts/{account_id}/logins` lists the
  main account's logins, oldest first: `id`, `username`, `enabled`,
  `household_disabled`.

Everything else about a login (password, email, limits, profiles, devices,
sessions, delete) uses the ordinary account routes with the login's own ID.

**Credentials** (`household_login_credentials_v1`):

- `require_password_change` on create and on move-profile makes the password
  temporary. The person chooses their own at their first sign-in, before they
  can do anything else. The web and the phone apps ask for the new password
  then; TV apps, Jellyfin-protocol clients and Audiobookshelf refuse the sign-in
  and tell the person to sign in to Silo first.
- An email address another account already uses, the main account's included
  and in any case, is refused before the login is made with `409 email_in_use`
  (`checkBloemHouseholdLoginEmail`). `duplicate` then means only a taken
  username. The address is not otherwise required: a login may have none.
- A login's address changes with the ordinary account update
  (`PUT /api/v2/admin/users/{id}` with `email`), and a password reset can make
  the new password temporary there with `require_password_change`.

**Password policy** (`storefront_password_policy_v1`): every storefront route
that sets a password follows the policy an administrator sets in Settings →
Sign-in (Silo #5124), as Silo's own password writes do. That covers platform
accounts, household logins and their move-profile, and tenant members on create
and reset (`tenancy.MemberService.SetPasswordPolicy`). bcrypt's 72 bytes stay
the upper bound under every policy. A refused password is `400 bad_request`
whose `code` names the rule with Silo's v2 field-error codes (#5356):
`password_too_short`, `password_too_long`, `password_needs_uppercase` or
`password_needs_special`. Its `message` is the rule's sentence. A storefront
that generates passwords reads the policy from the public
`GET /api/v2/auth/password-policy`. A tenant member create that replays an
earlier one returns that member even if the policy has changed since.

On Silo, 0 means unlimited for streams and transcodes. A login without
transcodes is written as `transcode_allowed: false` with no `max_transcodes`,
never `max_transcodes: 0`.

## Shared pool

A storefront can hold a household to its budget in two ways. With **shares**
it gives each account its own fixed limits that add up to the budget; the
server needs nothing more. With a **shared pool** every account of the
household draws on the same streams and transcodes at the moment it plays:

- The pool is a row on the main account (`bloem_household_pools`:
  `max_streams`, `max_transcodes`, 0 unlimited). It applies only while the
  main account has at least one login, so a household of one plays exactly as
  before.
- The playback limit provider names the household an account plays in
  (`withHouseholdPoolLimits`, `internal/api/bloem_household_pool_wiring.go`;
  `playback.HouseholdPoolForAccount`). A pool that cannot be read refuses the
  start, as a tenant lookup does.
- The fleet reservation (`internal/playback/bloem_household_reservation.go`)
  reads the account's household and its pool again inside every acquire's
  transaction, never from the request: a session reacquired after its lease
  lapsed, rebuilt after a restart or restored after a canceled replacement
  meets the pool as it is now, not the one it started with (none, for a
  session from before the first login or a move to a pool; the old size, after
  the pool was lowered). It stores the household on each reservation
  (`household_id`, the main account's id) and, for a household with a pool,
  takes the household's advisory lock after the account's and before a
  tenant's, counts the household's live reservations other than the one being
  made, and refuses a start the pool has no room for. The household's
  reservations are those made in its name and every live reservation of its
  main account and logins, so a session that started before the pool applied
  (the main account playing when its first login is made, or a household a
  storefront moves from shares to a pool) counts too. Every account's own
  limits still apply too, so a login can carry its own cap inside the pool.
- A refusal at the pool wraps Silo's own stream or transcode limit
  (`ErrHouseholdStreamsExceeded`, `ErrHouseholdTranscodesExceeded`), so
  clients, the stream-limit notice and lease renewal treat it as that limit.
  The native playback terminal keeps Silo's reason codes
  (`stream_limit_reached`, `transcode_limit_reached`), but its message says
  the household's limit is reached and that someone else in the household may
  be playing (`bloem_household_playback_refusal.go`). An account refused for
  its own limit keeps Silo's wording. Jellyfin compatibility answers with
  Silo's fixed `TooManyStreams` / `TooManyTranscodes` errors, which do not
  mention the account.
- Only the fleet reservation counts the pool. It leaves out the session being
  reserved again, so a session moving between direct play and transcoding
  keeps its stream in a full pool. A household session reserves even without
  an active profile, so nothing plays outside the pool.
- A transcode's reservation lives as long as its encoder. Client traffic
  renews a lease (two minutes), but a quiet paused transcode keeps its encoder
  until cleanup reaps the session (up to 30 minutes), so the 15-second cleanup
  pass also renews every transcode that still counts toward its account's
  transcodes (`renewHeldTranscodeReservations`). A transcode whose lease lapsed
  anyway and that the store no longer admits is ended as an idle session is,
  which stops its encoder. A held transcode also holds its stream in the pool,
  where in memory a quiet paused session frees its stream slot after two
  minutes: the pool errs on the side of what the household pays for.
- A session rebuilt after a restart never plays outside the pool. Silo admits
  a rebuild whose limits it cannot evaluate, without the limit gate. When the
  account plays in a pooled household and only its reservation failed (the
  store's 5-second timeout, a database blip), the rebuild is refused
  instead (`ErrHouseholdReservationUnavailable`). When the limits themselves
  could not be read, the household is unknown and the rebuild is admitted as
  Silo does, owing a reservation: the next cleanup pass that can read its
  limits reserves it against its household's pool, or ends it when the pool
  has no room (`reserveOwedSessions`). An account in no household keeps
  Silo's fail-open rebuild.
- Jellyfin-compatible sessions start through the same session manager as the
  signed-in account (a login signs in as itself), so they count against that
  account and its household, and they end with the main account like every
  other sign-in (above).

Under the platform credential:

- `PUT /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool`
  sets the pool: `max_streams` and `max_transcodes`, both required. A login
  (`household_nested`) and an admin account (`household_main_ineligible`)
  cannot have one.
- `GET` on the same path answers the pool with `streams_in_use`,
  `transcodes_in_use` (the live reservations of the main account and its
  logins) and `logins`; `404 household_pool_not_found` when there is none.
- `DELETE` on the same path removes it, and succeeds when there is none.

A restore brings a pool back with its main account (it is account policy).
Stock Silo does not enforce a pool: before it serves a switchable database,
`silo silo-switching prepare` holds the logins of every pooled household (see
[Silo switching](#silo-switching) below).

## Login cap

A household's login cap is the most logins its main account may have, counted
together whether they were made fresh or moved from a profile. The storefront
sets it beside the household's limits; without one, only the household's places
limit its logins (`bloem_household_login_caps`, one row per main account;
feature token `household_login_cap_v1`).

- Creating a login and moving a profile to a login both check the cap in their
  own transaction (`checkBloemHouseholdLoginCap`). Once the household has as
  many logins as the cap allows, the request is refused with
  `409 household_login_cap_reached`, whose message names both numbers, and
  nothing changes: the profile stays on the main account.
- The check locks the cap row, so two logins made at once under a cap are
  counted one after the other. A household without a cap locks nothing.
- Lowering the cap below the logins a household has leaves every login working
  and blocks new ones; the view reports `over_cap`. Moving a login back to a
  profile is never refused, so a household over its cap can always get back
  under it.
- A cap of 0 allows no logins.

Under the platform credential:

- `PUT /api/bloem/v2/admin/platform/accounts/{account_id}/household-login-cap`
  sets `max_logins` (required, 0 or more). A login (`household_nested`) and an
  admin account (`household_main_ineligible`) cannot have one.
- `GET` on the same path answers `max_logins`, `logins` and `over_cap`;
  `404 household_login_cap_not_found` when there is none.
- `DELETE` on the same path removes it, and succeeds when there is none.

The cap is account policy: a restore brings it back with its main account, and
it goes with the main account whichever server deletes it, so stock Silo's
delete is unaffected while Silo switching is on. Stock Silo makes no logins,
so it needs no cap.

## Moving a person

A person can move between a profile of the main account and a login of their
own, keeping everything the profile holds. Servers that support it advertise
`household_login_moves_v1`.

- `POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins/move-profile`
  creates a login as the create route does, but with no profile of its own,
  and moves the named profile into it as its primary profile. The main
  account's primary profile cannot move (409 `primary_profile`), nor can a
  profile with parental limits (409 `restricted_profile`): as its login's
  primary it would manage its own limits, and Silo holds a primary profile to
  no library limit.
- `POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins/{login_id}/move-to-main`
  moves every profile of the login onto the main account as ordinary profiles
  and deletes the login. It runs inside the account service's own delete
  transaction (`MutateAdminAccount`), so the login's sign-ins are revoked and
  its deletion audited as any account's are. A main account without room for
  the profiles answers 409 `profile_limit`; one that already has a profile
  named like one of the login's answers 409 `profile_name_conflict`.

Each move is one transaction: it completes, or both accounts are as they
were. A retry after a lost answer finds the profile or the login gone and is
refused (404) without changing anything; the storefront reads the household's
logins to learn that the move landed. A move never writes either account's limits; the storefront makes room
first (it owns the split of the household's limits, and reads a limit it did
not write as the operator's own).

**How a profile moves** (`MoveProfileInTx`,
`internal/userstore/pgstore/bloem_profile_move.go`). A profile's rows are keyed
by (account, profile), and the foreign keys to `user_profiles(user_id, id)`
neither cascade on update nor defer, so the profile row cannot simply change
account. The move takes both accounts' import locks in id order, then the
profile's history lock and its collections, then:

1. copies the profile row under the target account with the same id (its
   PIN, settings and restrictions with it); the copy passes the target
   account's profile limit like any new profile;
2. copies the profile's own collections, and the shelf groups they sit in
   (found by slug or label in the target account, or copied there), and moves
   their items and posters (a smart collage record is a cache that points at
   a poster by account, so it is dropped and the next list read rebuilds it);
3. removes what joins the profile to collections that stay, and other
   profiles to collections that move: sharing, allowances and sort orders
   cannot cross accounts;
4. carries the person's data to the target account, replacing any stray row
   already there for the profile;
5. moves the profile's section overrides between the accounts' values, its
   Jellyfin-compatible display preferences, and its Jellyfin-client
   configuration (`jellycompat:configuration:<profile>`);
6. ends what the old account's sign-ins had as the profile: sessions bound to
   it are revoked, Jellyfin- and Audiobookshelf-compatible sessions, push
   registrations, download device libraries, device settings, remote-control
   state and profile-bound API keys go, and so do watch-service links, whose
   tokens are encrypted for the account and profile they were made under (the
   person links the service again; the history it brought in is carried); a
   webhook sync connection that
   defaulted to the profile defaults to the old account's primary profile;
7. deletes the old collection rows and the old profile row.

What happens to each table with a profile column is in `bloemProfileMoveTables`
(carry, end, keep, follows) with its reason. Carried: watching (progress,
history, watchlists, ratings, dismissals, taste), settings and preferences,
reading and listening positions, recordings and recording rules, requests,
notification choices and inbox, and history import mappings.
Kept with the old account: records of what happened there (playback logs and
reservations, administrators' playback history, access decisions, diagnostic
reports, request events, the old devices' download change feed, watch-together
rooms). **`TestProfileMoveCoversEveryProfileTable` fails for a table with a
column ending in `profile_id` that the registry does not classify**, so a new
table, Silo's or Bloem's, is classified when it arrives.

**Playback across a move.** A move never lets a household play beyond its
pool: both accounts are the household's before and after it, and the pool
counts every live reservation of the main account and its logins, and those
recorded in the household's name. A session the person started under the old
account keeps its reservation there until it ends. After giving a profile its
own login, the handler announces the profile's ended sign-ins
(`OnUserProfileSessionsRevoked`), so every replica drops its cached
Jellyfin-compatible sessions as the profile. A stream the main account's
shared sign-in (one not bound to the profile) was playing for the profile
plays to its end under the main account, inside the pool. In shares mode the
storefront gives up the new login's share from the main account before the
move, and takes it back only after a move back.

An uploaded avatar keeps its stored key under the old account's
`profile-avatars/` prefix; it is served from the key in the row, and nothing
deletes an account's avatar prefix.

## Silo switching

Household logins and [Silo switching](silo-backend-switching.md) coexist.
Stock Silo, if it serves the database, knows nothing of households, so the
rules that keep a household safe live in the database:

- **Suspension holds under stock Silo.** Stock Silo suspends an account by
  writing `users.enabled`; the database switches the main account's logins
  off with it and ends their sign-ins where stock Silo checks them, and
  restoring the main account brings back only the logins the household
  switched off (invariants above).
- **Budgets hold under stock Silo.** Stock Silo plays each account to its own
  limits and does not enforce a shared pool. A household of shares fits its
  budget account by account. In a pooled household the main account carries
  the whole budget and each login may carry as much, so the person running the
  server runs `silo silo-switching prepare` before stock Silo serves: it holds
  every login of a pooled household (switched off, signed out, listed in
  `bloem_household_silo_holds`), and the household plays on its main account
  and profiles, within its budget. Bloem releases them when it starts
  (`silo silo-switching resume`): each comes back as its household is then,
  on under an enabled main account, switched off by the household under a
  suspended one. While held, restoring the main account does not bring them
  back, and an administrator's write of one under stock Silo takes it out of
  the hold.
- **Stock Silo's other writes fit.** It never names the household columns, so
  an account it creates is an ordinary account; a profile it creates on a
  login lands in the default organization; moving a main account to another
  group carries its logins (`users_household_logins_follow_group`); and
  deleting a main account deletes its logins.

`internal/auth/household_stock_silo_test.go` writes and reads as stock Silo
does; `internal/tenancy/silo_switching_households_test.go`,
`TestSwitchableAcceptsHouseholdLogins`, `TestSwitchableSiloDeletesAMainWithItsLogins`
and `TestBloemHouseholdWhenSwitchable` cover the rest.
