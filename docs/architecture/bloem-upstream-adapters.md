# Bloem adapters over Silo

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current reference, with sections corrected for the October 26 refactors. This
> Bloem page (first written in `e2e55f15e`, 2026-09-30; adapter-centred since
> `ae85b85b0`, 2026-10-05) explains how Bloem changes behavior without editing
> Silo files. A *seam* is a Silo-owned file that Bloem edits; each one is listed
> in `contracts/seams.txt`, which has 306 entries at this revision. An *adapter*
> is a Bloem-owned wrapper that keeps Silo's call shape. Several October 26
> changes retired adapters that earlier versions of this page described:
> `c60b7008e` (account policy back on `users`, one membership per account),
> `d064644a8` (membership created by the account repository), `b017bfe84`
> (lifecycle receipts removed), `0deddcf59` and `99a62d19f` (download profile
> from context, unreachable code removed). Silo's Jellyfin and Audiobookshelf
> servers came back on 2026-10-18 (`2393c41ee`). The Silo merge `68070f04b`
> (2026-10-27) changed the session lookups Bloem wraps; see
> [Realtime socket tickets](#realtime-socket-tickets) and
> [External sign-in and invitation transactions](#external-sign-in-and-invitation-transactions).

Bloem retains Silo's module path, Git ancestry, environment names, and protocol
identifiers. New fork behavior belongs in owned files, with small hooks in the
upstream files listed by `contracts/seams.txt`. The ledger records each shared path; `make verify-seams` checks that it stays accurate. See
[fork policy](../../FORK.md) and the [documentation index](../bloem/README.md).

## Migration filesystem

[`migrations.BloemFS`](../../migrations/bloem.go) wraps Silo's embedded migration
filesystem. The status, up, down, and normal startup paths in
[`cmd/silo/main.go`](../../cmd/silo/main.go) all use that wrapper. The migration provider applies the wrapper even to filtered migration
filesystems used for upgrade and rollback rehearsals. Historical SQL remains
unchanged; owned adapters handle the explicitly reviewed schema differences.

From 2026-08-29 to 2026-10-26 Bloem kept live account policy on
`organization_memberships`, through a phased authority recorded in
`membership_policy_authority` (`compatibility` and `finalized`, with `mirrored`
added on 2026-10-09 for Silo switching). Migration
`20261026003000_bloem_account_policy_on_users.sql` (`c60b7008e`) moved that
policy back onto `users` and retired the phases. The adapters below still matter
because they rewrite older Silo migrations, which an older database runs while
it is still in one of those phases. When upstream migration `20260926181741`
converts legacy blocked request limits on a finalized database, the adapter
updates `organization_memberships.requests_allowed` instead. The
legacy request limit applies to the account across its memberships; the migration
preserves that scope, increments affected policy revisions, and uses the
transaction-local policy-writer marker. Before finalization, the original account
update and existing authority fences remain in effect. The adapter does not
bypass those fences or finalize an installation itself.

The replacement requires an exact match for the expected upstream statement.
An upstream edit therefore requires review. Do not renumber migrations, mutate
the version ledger to skip a failure, or run the unadapted SQL on a finalized
Bloem database.

A database shared with Silo keeps Silo switching enabled (the single flag
`public.bloem_silo_switching` since `c60b7008e`; before it, the `mirrored`
phase). An upstream merge that adds a Silo write to a table Bloem constrains
needs a switching test beside the existing ones and a passing
`make silo-switch-check` against that upstream build. See
[Silo backend switching](silo-backend-switching.md).

Fresh empty-database migrations do not exercise the finalized-schema upgrade.
The regression builds that state, verifies both memberships of a blocked account,
and applies the remaining migrations:

```sh
SILO_REQUIRE_TEST_DATABASE=1 GOWORK=off go test ./internal/database \
  -run '^TestBloemRequestGroupMigrationAfterPolicyFinalization$' -count=1
```

Set `SILO_TEST_DATABASE_URL` to a disposable test database first. The fixture
creates and drops its own child database; never point it at a deployed catalog.

Silo's library access lists use `integer[]`. Bloem follows that representation
through tenant memberships, groups, entitlement revisions and rollback snapshots.
The former downstream widening migration is adapted to retain the upstream
width. Existing widened databases converge through
`20261014233826_bloem_silo_policy_alignment.sql`; any out-of-range value aborts
the transaction before data can be narrowed. No policy ID is clamped or discarded
to make a conversion succeed.

Silo's household limit is an inherited value: a null account override takes the
group limit, with an ungrouped default of five. An explicit account override wins
for ordinary groups. Bloem's managed entitlements additionally impose their
existing ceiling. The owned migration adapter preserves pre-existing group
columns, makes membership and rollback projections nullable, and translates
Silo's new limit representation in compatibility, mirrored and finalized modes.
The final alignment migration also covers a Silo-first database where Silo has
already recorded the upstream migration versions before Bloem's older owned
migrations run.

Library deletion keeps Silo's ordered cleanup and empty-array denial semantics.
Owned hooks apply the same cleanup to authoritative memberships and rollback
projections, including finalized schemas whose unused account fields were
renamed. Frozen guards admit only an exact cleanup of nonexistent library IDs;
other policy changes remain fenced. Already-adapted migration files carry a
versioned marker so filtered filesystems cannot apply an adapter twice.

## Profile-aware extensions

- [`SetProfileLimitProvider`](../../internal/playback/bloem_profile_limits.go)
  supplies profile-aware playback limits while the upstream `SetLimitProvider`
  signature stays available to ordinary Silo callers.
- Download policy keeps Silo's account-only helper signatures. Since
  `0deddcf59` (2026-10-26) Bloem's
  [download policy](../../internal/downloads/bloem_policy.go) takes the request's
  verified profile from the context when it belongs to the account being served,
  so Silo's v1 download handler and v2 download capability call Silo's
  `ServeDirect`, `ResolveDirectFile` and `Capability` unchanged. The former
  `downloads.CapabilityForProfile` extension was removed in `99a62d19f`.
  Tenant-aware group providers still require the validated request tenant.
  Ordinary Silo group providers retain their account policy and explicit user
  overrides without tenant context.
- The owned [`ServeDirectForProfile` and `ResolveDirectFileForProfile`](../../internal/downloads/bloem_direct.go)
  methods remain for an explicit download profile. The Jellyfin-compatible
  server uses them (`newBloemCompatDownloadServer` in `cmd/silo/bloem_compat.go`),
  and that explicit profile wins over the context.
- [`NewProfilePrepareAgainService`](../../internal/downloads/bloem_reprepare.go)
  is mandatory in Bloem HTTP retry composition. It checks the requesting
  profile's download and transcode authority before calling Silo's unchanged
  `PrepareAgain`. The delegated retry rechecks transcode authority and narrows
  its request-local configuration, so a replaced row or revoked permission
  cannot turn an allowed original retry into a forbidden transcode. Original
  and remux retries remain available when only transcoding is denied; server
  settings and the service's cached configuration are not mutated.
- Account and profile policy continue to be evaluated in the acting
  organization (an account has exactly one since `c60b7008e`). Server ownership, organization ownership, and a household's
  primary profile are different authority boundaries.

These adapters preserve upstream call shapes; they do not grant access when a
profile or organization policy denies it. Prefer the same pattern for later
upstream changes rather than widening every shared interface and test double.

## Playback tenant facts

[`NewBloemPlaybackAdmissionDecider`](../../internal/policy/bloem_playback_adapter.go)
wraps the unchanged upstream playback adapter. Its owned action checker resolves
trusted tenant facts from the request context for the requested user, replaces
caller-supplied facts, and denies admission before policy evaluation when that
context is missing, mismatched or inactive. Production router wiring must use
this owned constructor. The upstream generic constructor remains available to
ordinary Silo callers; it is not the Bloem admission boundary.

Tenant identity, membership revision, protected profiles, library ceilings and
quota reservations remain mandatory host authority. Optional presentation
plugins cannot disable those checks.

## Tenant group repository

[`TenantGroupStore`](../../internal/access/bloem_group_store.go) owns
organization-qualified group storage and profile-scoped resolution. Production
HTTP wiring, background policy readers and scenario execution must select
`NewTenantGroupStore`. Silo's `GroupStore` stays for account-level reads. Its
`GetPolicyForUser` and `GroupPolicyInTransaction` go through the seam in
`internal/access/group_store.go` to `bloemAccountGroupPolicy`, which reads the
account's `users.access_group_id` within the account's organization. It is not
a substitute for the profile-scoped `TenantGroupStore`. Tenant policy writes have one canonical owned
implementation; migrations and encrypted settings are unchanged.

[`TenantGroup`](../../internal/access/bloem_group_model.go) retains the common
upstream group plus organization, playback, profile-cap and managed-group fields.
Use its `Policy` method when evaluating account or profile policy. Project the
embedded common group only at the existing explicit group response boundary;
account effective-policy responses still include playback and profile limits.
Legacy group array encoding and v2 revision/conflict behavior remain unchanged.

The owned HTTP adapter resolves the validated organization for each call. All
five legacy group routes retain their existing authentication and an explicit
`RequireAccessGroupTenant` availability guard, returning `503 tenant_unavailable`
before storage when tenant context is absent. Inventory analysis resolves the
exact type-checked wrapper to its leaf handler while retaining full wrapper
provenance. The availability guard does not replace authentication.

Group updates preserve existing invalidation of accounts with assigned profiles;
accounts assigned only at account level retain their authorization revision.
Deletion moves and invalidates both the accounts and the profiles on the group.
(Before `c60b7008e` the account-level assignment was a membership column.) Empty updates preserve the
configuration revision; a nonempty same-value write advances it through the
existing timestamp trigger while retaining unchanged authorization revisions.
These are compatibility semantics, not new optional plugin behavior.

## Owned startup helpers

[`cmd/silo/bloem_main.go`](../../cmd/silo/bloem_main.go) owns native service
composition and shared-worker shutdown plumbing. The shared entry point retains
listener ownership, route registration and shutdown call order. Retired protocol
gateway constructors are not part of the current composition. Worker fencing precedes joins, and
cleanup follows only successful shutdown. Concrete nil pointers are normalized
before interface conversion. Tenant, authentication and quota enforcement remain
host authority, regardless of optional presentation evaluation.

## Owned build targets

[`Makefile.bloem`](../../Makefile.bloem) supplies Bloem-only targets and variables
through an additive include. Existing shared recipes retain explicit database,
scenario and contract hooks. The include precedes optional `Makefile.local`,
preserving local overrides and the upstream default target. Avoid duplicate
recipe overrides: they conceal upstream changes and make merge review harder.

## Embedded web

[`web/bloem-brand-plugin.ts`](../../web/bloem-brand-plugin.ts) owns product-copy
adaptation. [`web/product-brand-regex.ts`](../../web/product-brand-regex.ts)
handles the corresponding test regular expressions without renaming protocol
identifiers. The owned build adapter also maps the two built-in image references
and brands the public manifest and service-worker fallback notification title.
Development middleware and production asset emission provide the same behavior;
source public files and the shared brand component remain identical to upstream.
Custom image URLs and supplied notification titles are retained. Emitting the
worker into the bundle also lets the existing compressor generate matching
sidecars. Owned tests exercise actual development and build outputs. Upstream
changes to these explicit literals or public paths require adapter review. Bloem administration and Live TV pages load through lazy routes so
their code is not all part of the initial application load.

The launch bundle is checked by Silo's own `web/scripts/check-bundle-budget.mjs`,
which reports its size without gating it; Bloem keeps no separate allowance.
Seasonal branding reads are cached by server, while authenticated profile
ambience retains its own authority and scheduling rules.

Preview adapters in [`web/bloem-preview-plugin.ts`](../../web/bloem-preview-plugin.ts)
replace Silo modules at Vite resolve time, so TypeScript checks callers against
the original module and cannot see an adapter that drops an argument or prop.
Each adapter forwards every argument and prop outside preview mode. When an
upstream merge changes a redirected module, compare its signature with the
adapter; Silo's own tests for that module run through the adapter.

Bloem web code calling `/api/v1` routes without a generated v2 contract uses
`bloemV1Api` or `bloemV1ApiWithProfileRequestContext`, and `/api/bloem/v1`
routes use `nativeApi`. Silo's lint rule against `api<T>()` stays meaningful
for routes that have a v2 contract; do not alias these helpers to `api`.

## SQLite user-store versions

Silo owns the SQLite user-store version numbers in
[`internal/userdb/migrate.go`](../../internal/userdb/migrate.go), and a store
ends at Silo's `schemaVersion`. Bloem's idempotent repair step lives in
[`bloem_migrate.go`](../../internal/userdb/bloem_migrate.go) and runs inside
Silo's migration transaction without a version of its own. Bloem once shipped
a compatibility cleanup as version 32, before Silo claimed 32, and then as
version 34. A store Bloem left at 32 skipped Silo's version 32, so the repair
adds `user_devices.custom_name` when it is missing. Both cleanups dropped the
Jellyfin display-preference table, which the repair recreates. A store Bloem
left at its former version 34 or 35 has not run Silo's 34 or 35, so
`bloemSQLiteResumeVersion` resumes it after version 33 and Silo's idempotent
steps run. With no number of its own, the repair cannot collide with a later
Silo version.

## Realtime socket tickets

The v1 events, watch-together and playback-control sockets run Silo's
handshake unchanged since `a7436b4e5` (2026-10-25): the ordinary login
credential authenticates the socket, and Silo's `POST /api/v1/events/ws-ticket`
binds the profile and its PIN proof.
The v2 events and playback-control sockets keep Silo's ticket store and add
Bloem's tenant authority through Silo's `Validate` hook
([`bloem_socket_authority.go`](../../internal/api/handlers/bloem_socket_authority.go)).

## Recommendation scope

The recommendation engine builds cached rows under a scope resolver. Production
wiring wraps it in `newTenantAwareViewerResolver` like every other viewer
resolver; `TestProductionViewerResolversUseBloemTenancy` fails on an untenanted
one.

## Updating documentation and contracts

The GitHub front page and contributor guide are `.github/README.md` and
`.github/CONTRIBUTING.md`. Detailed differences live in owned architecture guides
and `docs/bloem/overlays/`. Root Silo documents can then remain identical to
upstream. When Silo removes a documentation page, fix Bloem's incoming links and
identify the replacement instead of describing a missing file as preserved.

Moving a fork note out of an upstream document may affect generated contract
digests. Regenerate and review the applicable artifact; do not treat a link fix,
successful build, or deployment probe as evidence that a contract gate passed.

## External sign-in and invitation transactions

Since `c60b7008e` (2026-10-26) Silo's account resolver and identity management
service (`account_resolution.go`, `identity_service.go`) are Silo's bytes
again: account policy is on `users`, so they read it as Silo does. The earlier
`userSource` projection over the membership join is gone.

Since `d064644a8` (2026-10-26) the account repository creates an account's one
membership itself (`linkAccountMembership` in `internal/auth/bloem_repository.go`).
It uses the organization the creating flow names, else the organization that
owns the account's access group, else the default organization. Silo's account
provisioner and its external sign-in providers (OIDC and directory) therefore
need no Bloem hook. The earlier membership-aware provisioner and
`CreateAccountWithMembershipInTransaction` (whose identifiers fed the lifecycle
receipts removed in `b017bfe84`) are gone. Invitation acceptance keeps one
hook, in the seam `internal/invitations/service.go`: it creates the account
with `CreateAccountForOrganizationInTransaction` in the invitation's stored
organization (`internal/invitations/bloem_service.go`).

Upstream's current-role session check also applies to Bloem. Since the Silo
merge `68070f04b` (Silo #3140) it also binds a token to its session's account:
`SessionRepository.ActiveSession` takes the account the credential names. Bloem's
preview-aware lookups, the administrative-context middleware and the
native-video recheck pass that account too, and Silo's own `IsValid` again
backs `LoginSessionActive`. A direct-profile
session retains the restricted `user` role even when its owning account is an
administrator. Initial setup sets both the owner and break-glass flags inside
the account-creation transaction, retaining an administrator recovery path when
local password sign-in is disabled. Provider identity and refresh-chain fields coexist with Bloem's
device, profile, credential revision, and authentication-method bindings.

Account invalidation retains the host's durable login-session revocation,
cache eviction and cross-replica notification boundaries. Post-commit work uses
the bounded detached context from `sessioninvalidation.Run`; profile-only
deletion keeps its scoped boundary. Jellyfin-compatible sessions follow Silo's
account-wide revocation, and `bloemProfileSessionRevoker` in
`cmd/silo/bloem_compat.go` ends those bound to profiles an organization removes
(see [protocol compatibility](bloem-compatibility-removal.md)).

Both invitation acceptance paths preserve the organization stored on the
invitation. Link invitations can receive an email at acceptance; supersession of
another invitation remains scoped to the inviting organization (the
organization filter is in `internal/invitations/bloem_repository.go`). Since
`b017bfe84` the v1 invitation and registration handlers are Silo's bytes again.
Bloem's lifecycle path, which had its own server-settings advisory lock, is
gone. The local-password check inside the acceptance transaction is Silo's
`EnsureLocalPasswordLoginAllowedInTransaction`.

The upstream user-detail page remains unchanged after its component split.
Bloem's Live TV permission is supplied by an owned permission list to the library
access card, sharing its group restrictions, conflict handling, and permission
rebasing. Telemetry extension coverage and the tenant-facts error declaration live
in owned files rather than creating additional upstream modifications.

The native DTO registry includes the imported request, watchlist, account and
trickplay response roots. The download episode wrapper serializes as a typed
episode object or explicit null; the graph generator preserves that wire shape
rather than exposing the server's internal validity flag. Generate these artifacts
with the Go toolchain declared in `go.mod`, since newer standard-library alias
representations can change how custom JSON types are inspected.

Email uses the same bundled Bloem wordmark as the sidebar when no custom
wordmark is configured. The owned mail initializer replaces the fallback bytes
and derived dimensions while retaining upstream's inline MIME content ID.


## Network sign-in metadata

The October 2026 integration imports network identity authentication ratified in
[Silo pull request 1828](https://github.com/Silo-Server/silo-server/pull/1828).
Eight admin plugin operations add `network` to their sign-in-mode response enum.
The pre-lock approval file records each operation and exact semantic fingerprint;
its `approved_in` references that upstream ratification, not a Bloem pull request.
No wildcard approvals or baseline changes are used.

Generated Android and Apple plugin DTOs store `sign_in_mode` as a string, so the
additional value does not require a decoding change. This is source inspection,
not native runtime acceptance. Network sign-in stays opt-in and does not change
password availability or override existing provider identity authority.

Native adoption remains follow-up work for both `bloem-android` and `bloem-apple`:
discover the capability, present the enabled network sign-in choice, and verify
the provider-unavailable and off-network paths. The generated DTO additions for
download preparation and subtitle sync likewise need explicit feature adoption;
generation alone does not establish that clients expose those controls.

## Owned deployment sample and compatibility constants

Bloem operators use `.env.bloem.example`, linked by the maintained Docker guide.
The upstream `.env.example` remains unchanged and selects the upstream image.
Moving the sample does not change a deployed environment or encrypted settings.
Since `2393c41ee` (2026-10-18) both samples document Silo's `JF_PORT` and
`ABS_PORT` for the restored compatibility listeners. The route-prefix constants
and same-origin gateway controls of Bloem's removal-era runtime are not active
configuration and should not be restored.

## Preparing Silo contributions

Keep two deliverables separate: owned Bloem behavior and small reusable upstream
hooks. The current section resolver registration and host-only `ExtensionData`
are candidate building blocks. Their generic declarations currently live in an
owned file, so an upstream proposal must extract neutral definitions and relevant
tests onto a clean Silo base. Assess layout/projection hooks as a separate slice;
do not include promotions, ambience, tenant schema, branding or fork build targets
merely because those features exercise the hooks.

A proposed hook needs a concrete Silo use case and proof that an absent extension
preserves existing behavior. Presentation extensions use host-authorized existing
catalog content; identity, profile/PIN, library and quota checks remain host
obligations. Validate the public contract and failure behavior for the exact
upstream slice rather than citing a passing Bloem deployment as upstream evidence.
These are candidates for discussion, not maintainer-approved interfaces.

Once an upstream hook lands, incorporate that exact upstream commit and retire
Bloem's corresponding shared-file difference. The tenant repository extraction
already reuses existing upstream interfaces; most of that extraction is maintained
Bloem code rather than new functionality to send to Silo.

## Silo player authentication

The web player keeps Silo's stream URL builder, subtitle inventory mapping,
subtitle loaders and audiobook playback hook unchanged. Media URLs carry the
access token using Silo's `token` query parameter; authentication keeps Silo's
header-first, query-fallback extraction. Bloem's session validation, direct-profile
route boundary, tenant identity and viewer policy still run after extraction.
Removing URL authentication without replacing every media loader caused subtitle
and attached-font requests to fail with 401. Follow upstream here rather than
adding a second subtitle transport.

## Native audiobook boundary

Native audiobook scanning and direct playback remain in the host. Silo's
Audiobookshelf-compatible server runs beside them on its own port, as described
in [protocol compatibility](bloem-compatibility-removal.md); it is not an
extension point for new providers. Current native publication and playback
invariants are in [bloem-native-audio-playback.md](bloem-native-audio-playback.md).
