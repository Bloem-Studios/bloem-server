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

- **A login is enabled only while its main account is.** `updateUser`
  (`internal/auth/repository.go`) is the one place an account's enabled state
  is written, and it calls the hooks in `internal/auth/bloem_household.go`.
  Disabling a main account, in the same transaction, disables each of its
  enabled logins, marks it `household_disabled`, bumps its access policy
  revision and revokes its sign-ins (login, Jellyfin-compatible and
  Audiobookshelf-compatible sessions, approved device sign-ins). The account
  service then ends the logins' live connections as it does the main
  account's. Enabling the main account enables only the logins marked
  `household_disabled`. Because Silo checks `users.enabled` wherever it admits
  an account, this one rule covers sign-in, refresh, device sign-in and the
  compatibility servers alike.
- **A login cannot be enabled while its main account is disabled**
  (`ErrHouseholdMainDisabled`). An administrator's own write to a login's
  enabled state clears `household_disabled`, so a login an administrator
  turned off stays off when the main account comes back.
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
  behind. A conditional delete checks the main account's revision before any
  login is deleted.

## API

Under the platform credential, beside the other platform account routes:

- `POST /api/bloem/v2/admin/platform/accounts/{account_id}/logins` creates a
  login: `username`, `password`, `profile_name`, `max_streams` (at least 1),
  `max_profiles` (at least 1), and optionally `max_transcodes`,
  `transcode_allowed` and `email`. It answers the account as the account
  service reads it, with `household_main_user_id` and `household_disabled`.
- `GET /api/bloem/v2/admin/platform/accounts/{account_id}/logins` lists the
  main account's logins, oldest first: `id`, `username`, `enabled`,
  `household_disabled`.

Everything else about a login (password, limits, profiles, devices, sessions,
delete) uses the ordinary account routes with the login's own ID.

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
  made, and refuses a start the pool has no room for. The household's reservations are those made in its name and
  every live reservation of its main account and logins, so a session that
  started before the pool applied (the main account playing when its first
  login is made, or a household a storefront moves from shares to a pool)
  counts too. Every account's own limits still apply too, so a login can carry
  its own cap inside the pool.
- A refusal at the pool wraps Silo's own stream or transcode limit
  (`ErrHouseholdStreamsExceeded`, `ErrHouseholdTranscodesExceeded`), so
  clients, the stream-limit notice and lease renewal treat it as that limit.
- Only the fleet reservation counts the pool. It leaves out the session being
  reserved again, so a session moving between direct play and transcoding
  keeps its stream in a full pool.
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
  Silo's fail-open rebuild. A household session reserves even without
  an active profile, so nothing plays outside the pool.
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
Upstream Silo never sees a pool in use: pools apply only to households, and
households never coexist with Silo switching.

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
were. A move never writes either account's limits; the storefront makes room
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

An uploaded avatar keeps its stored key under the old account's
`profile-avatars/` prefix; it is served from the key in the row, and nothing
deletes an account's avatar prefix.

## Silo switching

Upstream Silo does not know about logins: it would keep a login signing in
while its main account is disabled. So household logins and
[Silo switching](silo-backend-switching.md) never coexist.
`silo silo-switching enable` refuses while any account is a login, and while
switching is on, making an account a login raises
`bloem_silo_switching_household_login` (`users_household_login_switching_guard`,
which takes a share lock on the switching flag so a login made during an enable
is either counted or refused). Remove a database's logins before switching it
on. The two columns accept upstream Silo's writes: an account Silo writes never
names them and reads as an ordinary account.
`TestEnableSiloSwitchingRefusesHouseholdLogins`,
`TestSwitchableRefusesHouseholdLogins` and
`TestSwitchableAcceptsSilosAccountWritesBesideHouseholdLogins` pin this.
