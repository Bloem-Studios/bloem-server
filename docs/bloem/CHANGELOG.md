# Bloem integration changelog

This records Bloem's integration milestones. Silo's image build numbers and
upstream feature history remain separate from Bloem's application commits.

## October 27, 2026 — Three fixes from the OpenAPI review

The review behind the OpenAPI entry below listed handler problems it left
open. These three are fixed:

- **Organization invitations grant only the organization's own access.**
  `POST /admin/organization/invitations` accepted any `access_group_id` and
  `library_ids`. Nothing was exposed: the composite foreign key on
  `invitations (organization_id, access_group_id)` refused another
  organization's group, and a viewer's scope is always cut to the libraries its
  organization owns or is entitled to. But a foreign group surfaced as
  `503 tenant_unavailable`, and a library the organization can't use was stored
  on the invitation and copied to the account it created. Both now fail as
  `422 validation_failed`, on `access_group_id` or `library_ids`, before
  anything is written.
- **A direct-account entitlement apply is answered as it committed.** After
  the apply, `POST /admin/platform/accounts/{account_id}/entitlement/apply`
  compared the result's organization with the confirmation's and answered
  `409 confirmation_target_changed`, without an audit record, for an apply that
  had already been saved. The confirmed preview hash covers the organization,
  so in production an account that moved after its dry-run fails as
  `confirmation_stale` before anything is written. The late check could only
  turn a committed apply, or a replay of its key, into a conflict. It is gone,
  and `confirmation_target_changed` is no longer documented.
- **Releasing a Live TV session twice changes nothing.** The store released a
  session whatever its status. A second release, or a client's release after
  the stale-session sweep, moved `released_at` later, stopped the stream again
  and rewrote the session's playback history row (upserted by session) with
  the longer watch time. Only an active session is released now. A repeat
  release returns the session as it ended, which is what the OpenAPI already
  promised.

## October 27, 2026 — The native OpenAPI describes what each operation returns

`contracts/api/bloem/v1/openapi.json`, generated from the `internal/apiv2/bloem_*`
documents, was current with its Go source but no longer described the handlers.
`8fe96aadc` removed what was left of the lifecycle receipts that `b017bfe84`
deleted: sixteen administrative mutations still advertised an
`Idempotency-Key` header, `428 idempotency_key_required`, an
`Idempotency-Replayed` header and `503 lifecycle_request_pending`. Only the six
commands that carry their own key (the three entitlement applies and the three
policy-job routes) keep the header, as an alias for that key. Tenant retirement
on a server without the member service now answers `503 tenant_unavailable`
instead of `lifecycle_idempotency_unavailable`.

`67429b073` then checked all 213 operations against their mounts,
middleware and handlers:

- each operation lists the statuses and error codes its handler and middleware
  actually write, instead of the default 401/403/503 set, and drops statuses
  that cannot happen (for example 501 on Live TV, 429 on the bulk policy routes);
- 23 book-request administration operations named an `adminContextAuth`
  security scheme the document does not define; they now use
  `bloemOrganizationContext`, and the ebook setup operations also accept
  `bloemPlatformContext`, as the handler has since `c3523bb5f`;
- the native client operations (server identity, capabilities, organizations,
  Watch, progress sync, notifications, persons, item collections) were declared
  as v2 operations with `application/problem+json` errors; they are served by
  chi with the native `{error, message}` envelope and are now declared that
  way. `GET /organizations` returns `{"organizations": [...]}`, not an array;
- `BloemNativeError` gains the optional `code` that browse-only preview
  refusals add (`preview_only`, `preview_host`, `preview_session_invalid`,
  `preview_busy`).

No operation, path or operation ID changed, and no handler behavior changed
apart from that one error code. Since `3729418c9` Bloem CI runs
`make verify-bloem-openapi` and `make verify-bloem-api-catalogue`; neither was a
CI step before.

## October 27, 2026 — The standalone proxy serves hosted organizations; `latest` follows `main`

`a6d342390`: the standalone proxy's source check resolved every account through
the deployment's default organization, because a stream token, media grant or
download URL names an account but no organization. Since `c60b7008e` an account
has exactly one membership, so a member of a hosted organization was refused
(`404`) every source in its own libraries. The check now resolves the account
through its own organization, as a profile-less subject does elsewhere
(`tenancy.SubjectResolver`), and still fails closed for suspended members,
hidden libraries and accounts without a membership. See
[delivery boundary enforcement](../architecture/delivery-boundary-enforcement.md).

`6d0adcfe9`: Bloem Image now points `latest` at each new `main` image whose
commit is still `main`'s head, so `latest` follows `main` as Silo's does. Before
this, nothing moved `latest`: it was a September 24 build from the earlier
publishing path. Deployments that must be attributable still pin a full-SHA tag.
See [container image selection](overlays/wiki/deployment/docker.md#container-image-selection).

## October 27, 2026 — Production on CI's image; PostgreSQL tuning off

Production runs `276becf4a`. At about 06:58 UTC it moved from `d064644a8` to
`422a51fed`, the first production image built by Bloem CI and published by the
Bloem Image workflow (`ghcr.io/bloem-studios/bloem-server:<full sha>`), with
three forward-only migrations applied offline: Silo's
`20261026133552_markers_share_by_default_existing_installs` and
`20261026191107_allow_full_range_pq_download_tone_map`, and Bloem's
`20261026170000_bloem_cohort_coherence_ignores_deleted_libraries`. At 07:55 UTC
it moved to `276becf4a`, which changes only the web admin console and tests, with
no migrations. The PostgreSQL tuner had sized the database's CPU-based settings
for the application container's 32 CPUs instead of the database container's 12;
that was corrected first, and at 08:28 UTC auto-tuning was turned off
(`POSTGRES_TUNE=off`), because Bloem's database runs separately from the server
and the [Docker guide](overlays/wiki/deployment/docker.md#external-postgresql)
advises tuning off for an external database. The tuned values stay in the
database's `postgresql.auto.conf`. This is deployment evidence, not native-client
or playback acceptance; see the
[October 27 receipt](../operations/2026-10-27-main-deployment.md).

## October 27, 2026 — An admin context refused before its first render recovers

`276becf4a`: the web admin context provider registered its failure handler in
an effect, closed over the contexts of the last render. The first load sets the
contexts, mints the session and activates it in one continuation, so a request
refused before React rendered that update reached a handler that saw no
contexts, and a `tenant_session_required`, `insufficient_platform_authority` or
`organization_suspended` refusal sent the administrator to context selection
instead of re-minting or falling back. The contexts now live in a ref set
together with the state, and the handler reads it
(`web/src/contexts/AdminContextProvider.tsx`). A new test refuses a request in
that window. `AdminContextProvider.tsx` is a Bloem file; no seam changes.

## October 27, 2026 — Merge follow-ups: the client pin and a cohort test

Bloem CI on the merged `main` caught two things. Silo's AI provider connections
(#3106) add five administrative `/api/v2` wire types; `422a51fed` allowlists
them in `contracts/client/v1/coverage.json` like the other admin types (only the
bundled web admin reads them) and re-pins `contracts/client/v1/digest.txt` to
the merged tree. And the cohort tests in `internal/entitlements` compared
library sets while other packages in the same CI shard added and deleted
libraries in the same database; `f1d33d33f` runs that package in
`bloemtestdb.RunIsolatedSuite`, a database of its own, as `internal/preview`
already did. Neither changes runtime behavior.

## October 27, 2026 — Silo integration through 3e2d306b2

`68070f04b` merges 24 more Silo commits after `8b84639ed`, through `3e2d306b2`
(2026-10-27 03:06 UTC). Silo wins every conflict; the seam ledger stays at 306.
Most of the batch is security hardening: tokens are bound to their session's
account (#3140), v1 progress and stop refuse another profile's session (#3133),
signed stream and download tokens stay out of logs (#3137), as does the account
token in transcode relay logs (#3126), account-level
downloads check profile access (#3123), the default plugin cache refuses a
directory another local user controls (#3138), and image, settings, playlist,
webhook and Watch Together inputs are bounded. Features: AI providers are
connected once under AI Services and chosen per job (#3106), the embedding
provider is chosen there too (#3107), playback starts on an audio track the
client plays without conversion (#3115), and Meilisearch and the
Audiobookshelf port can be set from the environment (#3117).

Bloem adapts its own code to Silo's new behavior: Bloem's preview-aware session
lookups and the tenant admin context pass the account the credential names, so
the plugin router and auth middleware keep Silo's binding; Bloem's durable stop
follows Silo's `ownsPlaybackSession` rule, so a sibling profile cannot stop
another profile's playback; Silo's checked plugin cache directory feeds Bloem's
native-storage roots on the API host, the proxy host and the native worker; and
Silo splits manifest query validation out of `BuildPlaybackManifest` (#3129),
which Bloem's playback input-failure check follows. The generated client DTOs
gain `max_channels_by_codec`.

## October 26, 2026 — Silo's font extractor; a daylight-saving-safe test window

The merge through `8b84639ed` returned `subtitle_fonts.go` to Silo's bytes,
ending Bloem's temporary backport of Silo #2262 (font attachment extraction).
`d72d6d2d4` points Bloem's native-storage font extractor at Silo's command again
and removes the two Bloem tests that pinned the backported behavior.

Silo's `TestLogPartitionsMatchPartmanOnNonUTCServer` filled its window with
`now() + interval '8 days'` in America/New_York. PostgreSQL adds days at
wall-clock time, so across the US fall-back on November 1, 2026 that is eight
days and an hour, and a run after 23:00 UTC put the last hourly row into the
default partition: Bloem CI failed it while Silo's CI had passed the same commit
earlier in the day. `48513c1bd` uses 192 and 1,344 hours, which PostgreSQL adds
as absolute time. Partitioning itself is unchanged. This edits a Silo test file,
a temporary seam until Silo fixes the same, so the ledger grows from 305 to 306.

## October 26, 2026 — Silo integration through 8b84639ed

`303511a25` merges 25 Silo commits after `6de6d5e5e`, through `8b84639ed`. Silo
wins every conflict. Silo brings non-UTF-8 file names
([non-UTF-8 paths](../architecture/non-utf8-paths.md), #2571), intro-marker,
scanner and tone-mapping fixes, and web fixes. `subtitle_fonts.go` and
`contracts/api/v2/breaking-approvals.json` return to Silo's bytes: Bloem's copy
of the approvals carried 81 entries for changes `main` already included. Both
transcode starts keep Bloem's native-storage input hook around Silo's new
`fspath.CommandContext`. The seam ledger drops from 308 to 305.

## October 26, 2026 — A library a cohort group lists can be deleted

Deleting a library prunes it from every access group's `library_ids`, including
a template-managed cohort group, while the cohort's immutable revision keeps
listing it. The coherence checks compared the two lists exactly, so the delete
failed with SQLSTATE 23514 and no library any cohort group listed could be
deleted. `84624b9b3` adds
`20261026170000_bloem_cohort_coherence_ignores_deleted_libraries`, which compares
only libraries that still exist; dropping a library that still exists from a
cohort group is still refused. The migration is Bloem's; no seam changes.

## October 26, 2026 — The NFO listing cache waits for the change time

Silo's NFO provider kept a directory listing once the directory's modification
time was a tick old. A copy tool that restores the modification time leaves only
the change time moving, so a second change in the same tick could stay hidden
behind the cached listing; the self-hosted CI runners hit this on every run.
`63097f224` makes the settle rule use the later of the two times
(`internal/metadata/nfo/bloem_listing_settle.go`), and Silo's two caching tests
wait out the settle window (`4242849b6` gives those waits their lint reason).
`lookup.go` and `lookup_test.go` become seams, so the ledger grows from 306 to
308.

## October 26, 2026 — Go CI jobs split across runners

The three longest Bloem CI jobs run in shards (`52369dbad`, `5e6f9e407`): the
tenancy store's race tests in four "Tenant stores" jobs, `internal/auth` in three
"Native authentication" jobs, and `make test-go` in four "Go test" jobs, with the
database pins and regressions in jobs of their own. Each job has its own
PostgreSQL. `scripts/ci/deal.sh` deals packages and `scripts/ci/go-test-shard.sh`
deals a package's tests round-robin, so every test runs in exactly one shard.
Go test runs four packages at a time per database: sixteen at once against one
database collided on shared membership policy on the self-hosted runners. See
[CI](ci.md#jobs).

## October 26, 2026 — Bloem CI can run on self-hosted runners

Bloem CI's jobs carry the same runner routing as Silo's `trusted-linux-ci.yml`.
With the repository variable `BLOEM_SELF_HOSTED` set to `true`, pushes to
`main`, and pull requests by the logins listed in `BLOEM_CI_MEMBERS`, run on
Bloem's disposable self-hosted runners. Each job asks for its own run's
`bloem-run-<id>-<attempt>` and `bloem-job-<job id>` labels. Every other run,
including pull requests by anyone not listed (routing checks only the author's
login), and every job while the variable is unset,
runs on GitHub's `ubuntu-24.04` runners, which replace `ubuntu-latest` (now
Ubuntu 26.04) to match Silo. Workflow runs from all outside contributors now
need approval.

## October 26, 2026 — Invitation acceptance has one path

Accepting an invitation runs Silo's `Service.Accept` only. The transactional
`AcceptInTransaction` path the lifecycle receipts drove, with its locked lookup
and claim on the repository, has had no caller since the receipts went, and is
gone. An invitation still creates its account in the organization that issued
it. Organization invitations keep their own organization-scoped repository
methods: Silo's invitations have no organization, so reading one from the
request context would not return `repository.go` to Silo's bytes, and would let
Silo's admin handlers, which also issue admin invitations, act on an
organization. The seam ledger stays at 309.

## October 26, 2026 — Unreachable code removed

A `deadcode` pass over every command removed Bloem code no entry point reaches:
the window URL resolver, `adminpeople.WithMutationRequestID`, the unused
`promoted` opt-in reader, `downloads.CapabilityForProfile` (downloads read the
profile from context since the downloads change), `ParseServiceBaseURL`,
`outbound.PublicHTTPSPolicy`, the plugin pin check, `SessionCommandNames`,
ambience's `wire` and the never-mounted native storage v2 problem mapper with
its test. Test-only helpers moved into test files. Prairie Live TV files stay
as imported, and helpers that tests in other packages use stay where they are.
About 165 lines go; the seam ledger stays at 306.

## October 26, 2026 — One way to give an account its membership

Account creation now gives an account its one membership in a single place:
the account repository links it when the user row is written, in the
organization the creating flow names, else the one owning the account's group,
else the default organization. The `MembershipProvisioner` seam and the
account provisioner's separate membership paths are gone from Silo's
`router.go`, `service.go` and `main.go`. Tenant creation no longer deletes a
default-organization link before inserting the tenant membership: the account
is created for the tenant, and the tenant's slot fence adopts that link while
counting every other member. Silo's `account_provisioner.go`,
`invitations/service_test.go` and the web `profiles.ts` (which no longer sends
the `Idempotency-Key` the server ignores since the receipts went) return to
Silo's bytes, so the seam ledger drops from 309 to 306.

## October 26, 2026 — Web organization admin closer to Silo

The admin route table in `App.tsx` is Silo's again apart from two insertion
lines: platform pages no longer sit inside a wrapping `PlatformContextGuard`
route. AdminLayout's outlet (`AdminScopedOutlet`) applies the same guard to
every admin page except the organization pages, which keep their own
organization guard, and the context chooser. The organization invitations route
renders `OrganizationInvitationsTab` directly, so `InvitationsTab` and its test
are Silo's bytes again. The Rego policy page drops its second, in-page platform
check: the route guard already keeps organization admins out.
`useOrganizationLibraries` moves to the Bloem-owned
`hooks/queries/admin/organizationLibraries.ts`, so Silo's `libraries.ts` is
Silo's again. Silo's `App.requireAdmin.test.tsx` stays deleted: Bloem's
`RequireAdmin` admits organization admins through the administrative-context
handshake, which Silo's test cannot exercise. The seam ledger drops from 313 to
309.

## October 26, 2026 — Scenario rows and the bundle budget leave Silo's files

Bloem's scenario rows (profile login, device remote control, profile reads and
household commands) move out of Silo's `api-v1-auth`, `api-v1-devices` and
`api-v1-profiles` catalogs into `internal/scenariocatalog/bloem_catalogs`, and
`scenariocatalog.Load` splices them back onto the end of those catalogs, so the
merged catalogs and the adjudication digests are byte-identical to before. The
launch bundle check is Silo's report-only `check-bundle-budget.mjs` again; Bloem's
measured allowance and its wrapper are gone, so Silo merges stop rewriting a
budget file. The route inventory stays one generated file: Bloem's global
middleware changes the chain of almost every Silo route, so a separate Bloem
inventory would not return Silo's to its bytes. Three catalog seams go and
`catalog.go` becomes a one-line seam, so the ledger drops from 315 to 313.

## October 26, 2026 — Downloads read the request's profile

Silo's download handlers pass no profile. Bloem's download policy now takes the
request's verified profile from the context, when it belongs to the account the
download is for, so the v1 download handler and the v2 download capability call
Silo's own `ServeDirect`, `ResolveDirectFile` and `Capability` again. An explicit
download profile, such as a Jellyfin-compatible session's, still takes
precedence. `internal/api/handlers/downloads.go`,
`internal/apiv2/download_registry.go` and two v2 download tests return to Silo's
bytes, so the seam ledger drops from 319 to 315.

## October 26, 2026 — Lifecycle receipts removed

Bloem's Idempotency-Key replay system is gone: the receipt coordinator and its
four tables (`20261026030000_bloem_remove_lifecycle_receipts` drops them), the
rollout preflight, `cmd/lifecycleidempotencyctl` and its `make` targets, and the
receipt branch of every admin, tenant, people, entitlement, profile, settings,
registration and invitation handler. `lifecycle_idempotency_v1` and
`lifecycle_idempotency_required_v1` are no longer advertised, so bloem-admin-apple
and bloem-admin-android send their lifecycle mutations unkeyed, as they already
did against servers without the tokens. Mutations with their own command keys
keep replaying: tenant member creation, people and platform policy jobs,
entitlement applies and book requests. Every other mutation follows Silo's
retry-safety classes; see [retrying mutations](../bloem-api-reference.md#retrying-mutations).
Silo's `auth_registration.go`, `invitations.go`, `settings_values.go`,
`settings_values_admin.go` and the v2 `auth_sessions.go` and `profiles.go` return
to Silo's bytes, so the seam ledger drops from 325 to 319.

## October 26, 2026 — Bloem CI is its own workflow

Bloem's CI jobs move unchanged from its copy of Silo's
`.github/workflows/ci.yml` into the Bloem-owned
`.github/workflows/bloem-ci.yml`, named "Bloem CI". `ci.yml` is Silo's file again
apart from one line: its call to Silo's reusable `trusted-linux-ci.yml`, which
takes its tools from Silo's main branch and knows none of Bloem's gates, runs
only in Silo's repository. The seam shrinks from about 880 changed lines to one;
the ledger stays at 325. Bloem Image and the upstream sync merge now wait on
"Bloem CI". See [CI](ci.md).

## October 26, 2026 — Silo's public port; new organizations get the default catalog

The public port runs Silo's own composition again: `newRootHandler` and the
`http.Server` in `cmd/silo/main.go`, with Silo's compatibility listener
goroutines. Bloem's copy (`bloemRootHandler`, `publicServer`, `servePublic`,
`serveAux`), its route-inventory exclusion and its guard tests are gone; the
route inventory is unchanged apart from that exclusion.

A newly created organization, whether a platform administrator's or a
provisioned tenant, now receives the platform's creation-default entitlement
bundle in its creating transaction, so it sees the bundle's platform libraries.
Before, nothing applied the bundle and new organizations saw no platform
libraries. A re-provisioned tenant is adopted as it stands.

Series notification mutes now apply under Bloem's tenancy filter: the tenant
fanout query lacked Silo's mute check, so a muted series still notified.

Three swaps stay Bloem's after review. Silo's `netguard` does not refuse the
6to4, Teredo and other IPv6 special-use ranges `internal/outbound` refuses.
Silo's poll-based download quota lock still needs a second connection while
held, which Bloem's one-connection contention test rejects. And the fanout
tenancy join stays, because Silo's per-recipient access check turns a
suspended organization's recipients into a retried batch failure. The seam
ledger stays at 325.

## October 26, 2026 — Dead code removed

Code nothing called or ran is gone:
- the `playback.strict_reconstruct_admission` setting and its reconstruct hook,
  which was never set, so reconstruction always took Silo's fail-open path;
- the media-auth Prometheus counters;
- the `tenant_valid` rego rule, which only its own tests called;
- the web Live TV session start, heartbeat and release hooks, the unmounted
  "on now" home row, six unused `Policy*` types, and the Playwright specs no
  script or workflow ran, with the `@playwright/test` dependency.

With one membership per account, `AccountOrganization` reads that membership,
the unused `PrimaryMembershipSQL` projection goes, member deletion no longer
counts memberships, and an organization's revoke-all leaves an account that is
not its member alone. `action.rego`, `action_test.rego`, `permission_test.rego`
and `admin_user_policy_test.go` return to Silo's bytes; the seam ledger drops
from 329 to 325.

## October 26, 2026 — Presentation evaluators run in-process

Promotions and seasonal ambience evaluate inside the server again, with
`promotions.Engine` and `ambience.Engine`. The two bundled worker binaries
(`/usr/local/lib/bloem/plugins/promotions` and `.../ambience`), their
JSON-over-stdio bridge `internal/bloempresentation`, the
`BLOEM_PRESENTATION_PLUGIN_DIR` override and their build script are gone. The
workers ran only Bloem's own reviewed code, without an operating-system sandbox
or resource limits, so the process boundary isolated nothing. Host authority
and validation, and the rule that an evaluation failure omits optional
presentation, are unchanged. The Dockerfile returns to Silo's bytes, so the seam
ledger drops from 330 to 329; see
[presentation evaluators](../architecture/bloem-presentation-evaluators.md).

## October 26, 2026 — Music removed

Bloem's music catalog is gone: the `music` and `songs` library types, the
dedicated music scanner, the four `/api/bloem/v1/music/*` routes and the
`music_catalog_v1` feature token. `media_types` no longer lists `music_album`.
`20261026020000_bloem_remove_music` drops `music_artists`, `music_albums` and
`music_tracks` and any music libraries, albums and tracks; production had none.
Silo's `librarykind.go` returns to Silo's bytes, so the seam ledger drops from
331 to 330.

## October 26, 2026 — Account policy back on users

An account's policy (access group, permissions, libraries, quality, stream,
transcode, download and request limits, profile limit and policy revision)
lives on `users` again, where Silo keeps it. A membership only links an account
to its one organization: `organization_memberships` loses the 13 policy columns
and gains `UNIQUE (account_id)`, so making an account that already belongs to
one organization the owner or member of another is refused. The three-phase membership-policy authority,
its writer markers, rollout observations, heartbeat capability fence, rollback
snapshots and the `silo membership-policy` command are gone.
`20261026003000_bloem_account_policy_on_users` moves a finalized database's
policy back onto `users` and keeps a mirrored one switchable. Silo switching
is now one flag, `public.bloem_silo_switching`, driven by
`silo silo-switching {status|enable|disable}`; its guards and Silo-shaped write
placement are unchanged. Silo's `identity_service.go`, `account_resolution.go`,
`admin_users.go` and ten of its tests return to Silo's bytes, and the account
repository's Bloem hooks shrink to the membership link, the organization's
default group and the role sync. The seam ledger drops from 344 to 331.

## October 25, 2026 — v1 sockets use Silo's tickets

The v1 events, watch-together and playback-control sockets run Silo's handshake
again. Bloem's audience-ticket system and its two extra v1 ticket routes
(`/playback/sessions/{session_id}/control/ws-ticket` and
`/watch-together/rooms/{room_id}/ws-ticket`) are gone, and
`POST /api/v1/events/ws-ticket` is Silo's own route again, which requires a
profile. No Bloem client used the v1 sockets: the web app, bloem-apple and
bloem-android use v2, whose tenant authority is unchanged. The seam ledger
drops from 350 to 344.

## October 25, 2026 — Merge follow-ups: CI and the daily sync

CI on the merged main caught what the pre-push gates had not run. The eager
launch bundle grows 3,163 B, of which Silo's own bundle accounts for 2,903 B, so
Bloem's allowance moves to the measured 326,067 B. Go moves to 1.26.10 for
GO-2026-6809 (`crypto/x509`), because Bloem's CI scans on exactly the patch
`go.mod` names. Six changed-line lint findings on the merges' Bloem lines are
cleared; Bloem's split of an account update between the users row and the
membership moves out of Silo's `repository.go` into a Bloem-owned helper.

The daily upstream sync had failed since Silo changed a workflow file: GitHub's
default Actions token can never push or merge such a commit. The sync now pushes
and merges with the `UPSTREAM_SYNC_TOKEN` repository secret, and its checkout
keeps no credential while the merged upstream tree builds. See
[CI additions](ci.md#upstream-sync-merges).

## October 25, 2026 — Silo integration through 6de6d5e5e

Bloem merges 63 more Silo commits after `31fabc723`, through `6de6d5e5e`, with
Silo winning every conflict. The seam ledger stands at 350: Silo's planner file
returns to Silo's bytes, while the buffering version switch, two generated
decision fixtures and the breaking-change approvals Bloem's older main still
needs become seams.

The admin remote-control replan drops its `force` transcode mode, so Silo's
planner carries no Bloem input: Silo now lowers Auto quality itself when
playback keeps buffering, and `max_bitrate_kbps` below the source's bitrate
still forces a transcode; a `force` override is refused as an invalid payload.
The SQLite user store takes Silo's version numbers outright: Bloem's repair
step runs inside Silo's migration without a version of its own, and a store
Bloem left at its former v34 or v35 still resumes after v33.

Bloem adapts its own code to Silo's new behavior: library deletion recomputes
surviving series inside Bloem's deletion guard, buffering version switches and
version fallbacks preflight through Bloem's native-storage check, progress uses
Silo's shared client-clock clamp, and Bloem's catalog calls Silo's renamed
series recompute.

## October 23, 2026 — Silo integration through 31fabc723

Bloem merges 125 Silo commits after `b430099ff`, through `31fabc723`. Silo wins
every conflict; Bloem keeps only its integration points, and the seam ledger
drops from 388 to 347. Silo's webhook destination guard now covers what Bloem's
outbound override did (pinned dials, checked redirects, blocked addresses in
private mode), so the override is retired and both files return to Silo's bytes.
Silo's removal of low-value tests stands; the few Bloem tests that exercised
Bloem's own tenant code move to Bloem-owned files, and fixtures they shared with
removed Silo tests are copied beside them. Silo's `internal/database`
`TestMain` replaces Bloem's isolated-suite runner there. Silo's new database
tests that seed account policy on `users` seed it on the membership instead, and
the library-policy cases run through Bloem's tenant group store.

Bloem adapts its own code to Silo's new behavior: passwords and profile PINs are
hashed before a transaction opens (including Bloem's lifecycle and tenant paths),
a busy server answers 503, household profile limits are recounted inside the
creating transaction, Bloem's resolvers take the administrator's setting
defaults, library deletion moves shared collections inside Bloem's deletion
guard, and native remux carries the copied audio codec. The SQLite user store
keeps Silo's v34 and v35; Bloem's step becomes v36, and a store Bloem left at its
former v34 or v35 resumes after v33 so Silo's steps still run. Bloem's clients
are unreleased, so Silo's wire changes are taken as shipped and the generated
client contracts are regenerated.

## October 18, 2026 — Jellyfin and Audiobookshelf compatibility restored

Bloem runs Silo's Jellyfin and Audiobookshelf compatibility servers again, as
Silo ships them, on their own ports. This reverses the October 13 removal and
drops 464 deletion and removal seams from the ledger (852 to 388). Bloem's tenant
integration sits at the wiring points: tenant-aware scope resolvers, per-profile
downloads and sign-out, and hidden native-storage files
([protocol compatibility](../architecture/bloem-compatibility-removal.md)).
Bloem's companion enrollment and compatibility gateway stay removed. Bloem's
own `/api/v1/health` identity handler is retired in favor of Silo's. The SQLite
user store gains a v35 step that recreates the Jellyfin display-preference table.

## October 18, 2026 — Silo integration through b430099ff

Bloem merges 89 Silo commits after `ea3c10a7f`, through `b430099ff`. Silo's
implementation replaces Bloem's where it now covers the same need: recommendation
jobs take Silo's cluster locks, artwork downloads use Silo's guarded client, and
the storage-source note moved from Silo's library document into an
[overlay](overlays/libraries-api.md). Jellyfin and Audiobookshelf remain removed;
Silo files added under those packages are declared deletions.

Bloem adapts its own code to Silo's new behavior: account-scoped notification
state, the acting-administrator and PIN-proof rules on realtime sockets, the
local-network TLS listener on the public port, Silo's recommendation scope
resolver behind Bloem tenancy, and Silo's removal of Trakt, whose two v2
operations carry recorded approvals and leave the generated client DTOs. The
SQLite user store moves Bloem's step to version 34. See the
[adapter guide](../architecture/bloem-upstream-adapters.md) for the durable rules.

The eager launch bundle measures 322,627 bytes brotli, up 4,918 from the
317,709-byte allowance. Silo's own eager bundle grew 5,096 bytes across the same
commits (190,739 to 195,835), so the increase is Silo's and Bloem's share fell by
178 bytes. The allowance is regenerated for the combined source.

## October 15, 2026 — preview source integration

The browse-only preview implementation is integrated with the October Silo
source update. Protected preview policies, signed guest launch tickets, scoped
administration, catalogue sanitization and the web subscription flow are now
present together. The combined session lookup preserves remote-playback purpose
and profile binding, and the current collections page uses a read-only preview
adapter. Ordinary subscribers retain the upstream collection workflow.

The native OpenAPI, v2 OpenAPI, route inventory, migration ledger and generated
Kotlin/Swift contracts include the combined surface. See the
[wire reference](../bloem-api-reference.md) and
[preview invariants](../architecture/bloem-browse-only-content-policy.md).
Garden supplies the separately configured launch flow; publishing source does
not enable a live preview or add native-client preview controls.

The preview seed supports rollback and reapplication without losing curated
template history, while still refusing an operator-owned reserved-key collision.

The combined production web build measures 317,709 bytes brotli at eager launch,
with zero cross-origin render-blocking resources. The published upstream-integrated
build measures 315,164 bytes; preview safeguards add 2,545 bytes (0.8%). The
measured allowance is regenerated for the combined source rather than retaining
the older branch's stale measurement.

## October 15, 2026 — native OpenAPI coverage

The [native OpenAPI artifact](../../contracts/api/bloem/v1/openapi.json) now
describes 196 operations, adding the 92 previously missing administration and
Live TV operations. The [catalogue](api-catalogue.md) reports zero explicit
inventory gaps; the wildcard router mount remains a dispatcher.

Declarations follow existing chi handlers: scoped administrative tokens and API
keys, JSON revisions, lifecycle receipts, selected-profile authority, signed HLS
tickets, DVR and raw media responses. Production mount tests compare allowed
methods, aliases and trailing slashes. Schema tests cover native policy nulls,
decoder defaults and actual Live TV serialization. This changes documentation
only; runtime behavior, the Silo v2 contract and client releases remain separate.

## October 15, 2026 — documentation refresh

Developer references and the portable operator/viewer guides now follow the
October 14 source integration. Removed protocol services are no longer offered
as installation or client choices. The profile-inheritance, 32-bit policy-array,
mixed library-location, SDK and generated-contract references match that source.
Historical deployment records retain their original evidence.

Both OpenAPI artifacts, web API types, settings bindings, route inventory and
client DTOs were regenerated or checked against their source registries. The
native OpenAPI metadata links to the wire reference and identifies its coverage
boundary. The new [API catalogue](api-catalogue.md) inventories typed operations
and exposes remaining native schema debt instead of treating chi dispatchers as
operations. `make verify-bloem-api-catalogue` checks reproducibility and its
method-dispatch regressions.

The [documentation workflow](documentation-workflow.md) records the reviewed
Silo manual structure and Bloem's adaptation: developer invariants, API references,
task-based operator/viewer guides, generated contracts and dated deployment
records. This documentation refresh neither publishes a manual site nor deploys
a server or adopts contracts into a native client release.

## October 14, 2026

Local source integration: all 120 Silo commits after `1747a126b` through
`ea3c10a7f06f82d15cb46423eee7606fe987f7f7`. The normal merge preserves upstream
ancestry. This record does not describe a deployment.

Shared behavior follows Silo, including signed worker requests, node draining
and recovery, remote-playback profile and expiry restrictions, inherited
household limits, local metadata, plugin event delivery, and the reorganized
settings navigation. Bloem retains its tenant, native storage, Live TV and
branding integration points. Jellyfin and Audiobookshelf runtime removal remains
in force; each incoming retired path is declared individually.

Library policy arrays converge to Silo's 32-bit `integer[]`, including membership
and entitlement rollback projections. Conversion rejects overflow atomically.
Historical migration SQL remains unchanged: owned filesystem adapters handle
schema differences, and an additive alignment migration also covers databases
that Silo migrated first. Ordinary account profile-limit overrides retain Silo's
precedence; managed Bloem entitlements keep their explicit ceiling. See the
[adapter contracts](../architecture/bloem-upstream-adapters.md).

The existing deeper S3 and Bookwarehouse folder picker survives the integration.
Its browser acceptance covers three remote folders plus three filesystem paths
in one library at phone and desktop widths. Generated API, settings and native
client contracts remain in the server repository; sibling client checkouts are
not modified by generation.

Verification includes the full Go suite (219 passing packages), 8,446 web tests,
49 final navigation tests, the production web and server builds,
four folder-picker browser cases, 136 database query pins, 155 database contract
pins and 77 mixed-library pins. The final server binary passed backend switching
against the exact incoming Silo source. Generated contracts, migration validation,
protocol removal, route recovery and local-path checks passed.

Changed-line lint reports three unchecked-error calls that are byte-for-byte
upstream: `internal/metadata/local_person_photos.go` and two calls in
`internal/metadata/item_extra_artwork_test.go`. They remain recorded upstream
findings; no lint rules are disabled. Native client releases still require the
[contract transition check](../architecture/bloem-contract-adjudications.md).

## October 9, 2026

Source milestone, not yet deployed: **Silo backend switching.** A Silo database
taken over by Bloem can be handed back and forth. The new `mirrored`
membership-policy phase keeps Silo's `users` policy columns and Bloem's
default-organization membership identical in both directions. It also covers
group moves (profiles follow), roles, and ungrouped admins. Silo-shaped
writes are placed in the default organization: sessions without a device,
profiles without an organization, and accounts without a membership.

`silo membership-policy enable-silo-switching` enters the phase. Finalizing from it
requires `--end-silo-switching`. The phase is limited to one organization and no
profile logins. Account creation now commits the account and its membership
together.

`make silo-switch-check` passed with upstream Silo build 1216
(`sha256:4400eab1…`, revision `454c94f45`) and the branch's Bloem image. It covers
Silo → Bloem → Silo → Bloem, each backend reading the other's writes. Focused
package suites, changed-line lint, migration validation and the seam check
passed; a fresh whole-branch review's critical and important findings were
fixed with tests. Known, unrelated: Bloem's admin user delete returns 500 after
committing the delete.

## October 7, 2026

Bloem `c87b44545` is deployed with all 27 Silo commits through `74158b4a8`,
preserved through a normal merge. Native EPUB/PDF onboarding adds protected
source management, durable pathless-library recovery, binding, queueing,
authorized publication and reader delivery. Retained source/catalog identities
survive disable and uninstall; unsupported mutations refuse explicitly.

The Silo merge brings shuffle, episode download poster fields, download queue
admission, autoscan delivery/polling improvements, overlap waiting and auth/catalog
fixes. Bloem preserves canonical tenant authority and private native guards.
The declared seam count is 407; the final lint cleanup adds no seams.

Go/web builds, focused tests, real disposable-database regressions and uncapped
changed-line lint passed within their recorded scopes. Migration completion,
health/readiness, web assets and administrative refusals were checked on the
exact running binary. No database backup was taken for this explicitly approved
rollout. [Deployment evidence and limits](../operations/2026-10-07-native-storage-deployment.md)
remain distinct from production provider and native-client acceptance.

Bloem plugin SDK `v0.24.0` is published with the Silo `v0.23.0` public protocols
and Bloem storage service retained. Native OpenAPI now documents all 32 storage
administration operations, verified against their production route registration.
Generated Kotlin and Swift contracts include the native request/response shapes
and merged upstream fields. SDK versions, generation and client adoption are
tracked in the [compatibility guide](../architecture/bloem-sdk-compatibility.md).
Documentation and generated-binding updates do not by themselves redeploy the
server or enable unsupported native features.

## October 5, 2026

A larger tenant extraction subsequently restores three more complete upstream
production files, reducing declared seams from 390 to 387. Organization-qualified
group storage, extended policy fields and HTTP context adapters now live in owned
files; upstream group handlers and service interfaces remain unchanged. Four
startup helpers move to owned wiring. Across all affected shared production Go
files, the upstream diff falls from 4,930 to 4,450 changed lines, including caller
and inventory-hook costs. Existing revision semantics, DTOs and mandatory tenant
authority are preserved. This is owned host-module extraction, not optional
multi-tenant enforcement or SDK catalog packaging.
The combined cutover is deployed at `d55ebd6d` with passing health, contract,
protected-denial, branding and worker checks. See the
[merge-reduction verification record](../operations/2026-10-05-merge-reduction.md).

A subsequent merge-reduction pass restores six upstream files byte-for-byte,
reducing declared seams from 396 to 390. Trusted playback tenant facts move to
an owned checker; branding is supplied by the existing owned build adapter,
including development public assets. The codec capability declaration moves to
its owned helper. An additive Make include reduces the shared Makefile diff
from 278 to 61 changed lines while preserving its commands and verification.
Tenant isolation remains mandatory host authority. SDK catalog packaging is
still deferred; this pass measures upstream-file reduction rather than plugin
count. See the [adapter guide](../architecture/bloem-upstream-adapters.md).

Promotions and ambience evaluation move into bundled local plugin workers with
host-owned authority and storage. Shared sections use generic extension hooks;
API projections and existing records are retained without a migration. This is
local worker extraction, with catalog SDK integration deferred. The seam count
remains 396, and IPTV/Xtream extraction remains shelved. See the
[cutover verification record](../operations/2026-10-05-presentation-plugin-cutover.md).

The upstream merge imports Silo `1555e5214` (build 1116), including network
identity sign-in, subtitle synchronization, download preparation controls,
player recovery, local discovery, administrator policy defaults, and catalog
fixes. Bloem retains membership/profile policy and public compatibility behavior.
The seam ledger stays at 396 paths; deployment samples and compatibility
constants move to owned files to offset necessary integration adjustments.

The [verification record](../operations/2026-10-05-upstream-integration.md)
distinguishes passing build/contract/fixture checks from inherited full-suite
failures and native feature-adoption follow-ups.

## October 2, 2026

The normal upstream merge includes Silo `bbf12add2`: external sign-in and device
approval, link invitations, branded email, trickplay, subtitle timing, download
improvements, and dependency fixes. Bloem keeps 396 declared seams, with owned
adapters for membership-aware account transactions, current policy reads, initial
owner recovery, invitation admission, Live TV permissions, and email branding.

Generated client DTOs, settings bindings, OpenAPI, and fixtures are refreshed.
The [integration verification record](../operations/2026-10-02-upstream-integration.md)
records passing checks and the remaining full-suite limitations.

## September 30, 2026

Bloem `b2ad352a0` includes Silo upstream `8e2e84047`. The source merges preserve
upstream ancestry and retain 396 declared upstream-file seams.

### Inherited from Silo

The imported source includes account password-reset links and temporary-password
handling, age-normalized content restrictions, local and remote stream bitrate
limits, request routing and group limits, request follows and season selection,
real-time library monitoring, theme-song playback, and title-based watchlists.
These are upstream capabilities; use the relevant API's capability discovery
and the client's implemented workflows rather than assuming every client exposes
every feature.

The final follow-up adds bounded retries when a Trakt paginated listing changes
during a read: at most three attempts, restarting the inconsistent listing.
Other failures are returned without that consistency retry.

### Bloem integration

- Preserved organization membership authority and profile-aware policy through
  owned adapters, including the finalized-schema request-group migration fix.
- Retained account ownership fields during setup and shared profile readers
  during transactional lifecycle operations.
- Preserved the upstream playback/download call signatures with profile-aware
  Bloem extensions.
- Kept native deployment-readiness decoration and library relink behavior in
  the fork's integration hooks.
- Moved more web code behind lazy routes and kept Bloem's bundle allowance
  separate from upstream validation rules.

See [integration adapters](../architecture/bloem-upstream-adapters.md) for the
implementation boundary and [deployment and validation](../operations/2026-09-30-upstream-deployment.md)
for the exact running revision, migration correction, passed checks, and open
contract/scenario/UI validation work.

The later documentation refresh updates the GitHub README, contributor and
operator guides, API overlays, historical checkpoints, and links after upstream
documentation moves. A documentation commit does not replace the deployed binary.
