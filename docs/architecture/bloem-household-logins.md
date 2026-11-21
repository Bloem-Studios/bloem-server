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
- The playback limit provider adds the pool of the household an account plays
  in (`withHouseholdPoolLimits`, `internal/api/bloem_household_pool_wiring.go`;
  `auth.HouseholdPoolForAccount`). A pool that cannot be read refuses the
  start, as a tenant lookup does.
- The fleet reservation (`internal/playback/bloem_household_reservation.go`)
  stores the household on each reservation (`household_id`, the main account's
  id) and, for a household with a pool, takes the household's advisory lock
  after the account's and before a tenant's, counts the household's live
  reservations other than the one being made, and refuses a start the pool has
  no room for. The household's reservations are those made in its name and
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
  keeps its stream in a full pool. A household session reserves even without
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
