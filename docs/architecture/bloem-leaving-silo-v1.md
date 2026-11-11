# Leaving Silo's `/api/v1`

> **Provenance and status.** Bloem page, rewritten on 2026-11-01, checked against `main`
> at `b6039036d`, and brought up to date on 2026-11-11, the day Silo retired `/api/v1`
> (Silo #3200, merged into Bloem in `26cf9b3e4`). It records the owner's decision of
> 2026-10-31 that Bloem should not depend on Silo's `/api/v1`, how Bloem left it, and what
> happened to the routes Bloem still had there. It names the retired paths as history. The
> technical rules are in the [For developers](#for-developers) section.

Silo has retired its oldest API. Every route there answers `410 Gone` with
`client_upgrade_required`; only the health and readiness probes stay. Bloem's own routes
there went with it, and nothing in Bloem calls it. Bloem's own API is `/api/bloem/v2`. This
page explains why that mattered, the rule Bloem follows, who moved and when, and where each
old route went.

## Why

Silo had two native APIs. **`/api/v1`** was the old one: frozen, kept for a while so older
apps kept working, then retired on 2026-11-11. **`/api/v2`** is Silo's stable API. Since the
retirement, `/api/v1` routes answer "gone, please upgrade"; only its health checks stay.

Bloem takes Silo's code as it is (Silo wins every merge). So when `/api/v1` went, every Bloem
route mounted on it went too, along with any Silo `/api/v1` code Bloem still reused. That is
why Bloem moved off it first.

## The rule

![A decision: if Silo's /api/v2 has the route, call it; if not, add a route under /api/bloem/v2; never add to /api/v1](images/leaving-silo-v1/new-route.svg)

1. **Bloem adds nothing to Silo's `/api/v1`, and calls nothing there.**
2. **For anything Silo has, Bloem's callers use Silo's `/api/v2`.**
3. **For Bloem's own features, or where `/api/v2` lacks something Bloem needs,** Bloem adds a
   route to its own API, **`/api/bloem/v2`**, instead of reaching back to `/api/v1`. The
   first was creating and updating an account on a plan.
4. **Bloem's own API keeps its own version number.** It moved from `v1` to
   **`/api/bloem/v2`** on 2026-11-11, with no alias, and now changes version only for a
   breaking change to Bloem's own contract. It grows by adding fields and routes, and apps
   find new ones through a list of **feature tokens**.

## Who has moved

![A grid of callers against Silo's /api/v1, Silo's /api/v2 and Bloem's /api/bloem/v2, showing who called /api/v1 and that it now answers 410 Gone](images/leaving-silo-v1/callers.svg)

- **The storefront (Bloem Garden)** left `/api/v1` on 2026-10-31. Organizations, accounts on
  plans, announcements, campaigns and seasonal packs now use Bloem's API; plain accounts,
  libraries and playback sessions use Silo's `/api/v2`.
- **The web app** left on 2026-11-01. Campaign cards, announcements and the seasonal look of
  the signed-out pages now come from Bloem's API; the rest goes through Silo's own `/api/v2`
  web code.
- **Bloem's phone and TV apps** still called some `/api/v1` routes when Silo retired it:
  campaign cards, the signed-out seasonal packs, remote control and profile sign-in. Those
  calls now answer `410 Gone`. Campaigns and seasonal packs have native replacements; remote
  control and profile sign-in are dormant until native routes are built.
- **Silo's own apps** have already left: on 2026-10-31 `silo-android` called none of
  `/api/v1`, and `silo-apple` only its health check.

## Example: one campaign card, before and after

This example is made up.

The platform runs a "Winter Film Week" campaign. Before 2026-11-01 the web app showed its card
on Home by asking Silo's `/api/v1` home layout for a "promoted" row, and hid it with a Silo
`/api/v1` "dismiss" call. Now the web app asks Bloem's `/api/bloem/v2/promotions` for Home's
cards, which come with the row they belong in, and hides one with a Bloem call that writes the
same per-profile dismissal. A viewer sees exactly the same card. Since 2026-11-11 the old way
answers `410 Gone`.

## Who can change what

| Who | What they manage |
| --- | --- |
| The owner | The decision to leave `/api/v1`, and which dormant feature gets native routes next. |
| Bloem developers | New routes go under `/api/bloem/v2`, never on `/api/v1`; a test fails if a `/api/v1` route is added, and `make verify-no-v1` fails if a Bloem line names a retired path. |
| App developers (storefront, web, phone) | Calling only Silo's `/api/v2` and Bloem's API. |

## Words used in the code

| On this page | In the code and repository |
| --- | --- |
| Silo's old API | `/api/v1`, retired on 2026-11-11 (Silo issue #886, Silo #3200) with `410 Gone` and `client_upgrade_required` |
| Silo's stable API | `/api/v2` |
| Bloem's own API | `/api/bloem/v2`, typed routes with OpenAPI declarations |
| Feature tokens | `feature_tokens` in `GET /api/bloem/v2/capabilities` |
| Every mounted route | `contracts/api/v2/route-inventory.json` and the [API catalogue](../bloem/api-catalogue.md) |
| The `/api/v1` route test | `internal/api/v1_route_surface_test.go` and its golden lists |
| The retired-path guard | `scripts/bloem/verify-no-v1.py` (`make verify-no-v1`), with its allowlist `contracts/bloem-v1-allow.txt` |

## For developers

- **API:** the [API by topic](../bloem/api/README.md) lists every typed `/api/bloem/v2`
  operation. The [Bloem routes on Silo's `/api/v1`](../bloem/api/v1-routes.md) page records
  the retired routes, each with its replacement.

### Why, in detail

Silo's `/api/v1` was a frozen alpha contract. A maintainer decision (Silo issue #886)
retired it in Silo #3200, merged into Bloem in `26cf9b3e4` on 2026-11-11: the main listener
answers every `/api/v1` route except `/api/v1/health` and `/api/v1/ready` with `410 Gone`
and the `client_upgrade_required` problem code (see Silo's [API contract](api-contract.md)).
Silo wins every merge, so every Bloem route mounted beside it went too, along with the Silo
v1 handler code Bloem reused.

### The rule, in detail

- **Bloem adds nothing to Silo's `/api/v1`.** A Bloem feature gets a typed route under
  `/api/bloem/v2`, with an OpenAPI declaration, so it appears in the
  [API by topic](../bloem/api/README.md).
- **Bloem's callers use Silo's `/api/v2`** for everything Silo has, and `/api/bloem/v2` for
  Bloem's own features.
- **Where Silo's `/api/v2` lacks something Bloem needs,** Bloem adds a native route rather
  than calling `/api/v1`. The first was creating and updating an account on a plan.
- **Bloem's own API is `/api/bloem/v2`.** Its version number is Bloem's, not Silo's. It was
  `/api/bloem/v1` until 2026-11-11, when it moved to `v2` with no alias; its identifier is now
  `"api": "bloem/v2"` and `bloem_api: ["v2"]`, and migration
  `20261111215029_bloem_api_v2_ambience_asset_paths` moved the artwork paths stored in
  Bloem's tables. It grows by adding fields and routes, which clients detect through
  `feature_tokens` in `/api/bloem/v2/capabilities`, and changes version only for a breaking
  change to Bloem's own contract.

### Done: the storefront (2026-10-31)

Every storefront call to a Bloem server moved off `/api/v1`.

| Storefront calls | Was | Now |
| --- | --- | --- |
| Reseller organizations and their members | `/api/v1/admin/tenants/...` | `/api/bloem/v2/admin/platform/tenants/...` |
| An account's profiles, devices and sign-in sessions | `/api/v1/admin/users/{id}/...` | `/api/bloem/v2/admin/platform/accounts/{account_id}/...` |
| Creating an account, or re-applying it on a retry, with a plan | `POST`, `PUT /api/v1/admin/users` | `POST`, `PUT /api/bloem/v2/admin/platform/accounts`, built on Silo's `/api/v2` account service |
| Account list, read, enable, password, policy, delete | `/api/v1/admin/users` | Silo's `/api/v2/admin/users`, with `If-Match` on writes |
| Libraries, playback sessions | `/api/v1/libraries`, `/api/v1/admin/sessions` | Silo's `/api/v2/libraries`, `/api/v2/admin/sessions?user_id=` |
| Announcements | `/api/v1/admin/notifications/announcements` | `/api/bloem/v2/admin/platform/announcements` |
| Campaigns and seasonal packs | `/api/v1/admin/promotions`, `/api/v1/admin/ambience` | `/api/bloem/v2/admin/platform/promotions`, `.../ambience` |
| Uploaded artwork paths | `/api/v1/ambience/assets/` | `/api/bloem/v2/ambience/assets/` |
| "Does the server support playback cards?" | `/api/v1/notifications/capability` | `playback_overlays_v1` in `/api/bloem/v2/capabilities` |

The native platform routes take the storefront's credential, an enabled administrator's
unscoped API key, as well as a Platform administrative context, and recheck the account on
every request. The same routes now also serve an organization's domains and branding
(`/api/bloem/v2/admin/platform/tenants/{id}/domains` and `.../branding`, advertised by
`organization_domains_v1`). The storefront's Silo adapter also serves plain Silo servers,
which now get the same `/api/v2` calls; a plan requested against a plain Silo server is
refused before anything is created.

Silo's `/api/v2` writes need `If-Match` with the account's current ETag, so each storefront
write to an account is a read and then a write, and a write whose ETag went stale is read and
retried once. That doubles the requests a write makes, which a server's API-key rate limit
counts, so the storefront waits out a `429` (its `Retry-After`, at most 10 seconds) and sends
the request again, at most twice.

### Done: the web app (2026-11-01)

The web app's last calls to `/api/v1` moved, and its Bloem client lost the helper
(`bloemV1Api`) that reached `/api/v1`. Its callers use `nativeApi` and
`nativeApiWithProfileRequestContext` (`web/src/api/bloemClient.ts`) for Bloem's API and Silo's
own web functions (`web/src/api/v2/`) for Silo's.

| Web app calls | Was | Now |
| --- | --- | --- |
| Campaign cards on a title, before playback and on Home | `GET /api/v1/promotions`; for Home, `/api/v1/home/layout?promoted=1` and the `promoted` row's items | `GET /api/bloem/v2/promotions`, which also serves Home, with `home_position` for the row it goes in |
| Hiding a campaign card | `PUT /api/v1/home/dismissals/promo:{surface}/{id}` | `PUT /api/bloem/v2/promotions/{id}/dismissals/{surface}`, writing the same per-profile dismissal |
| Saving an in-playback campaign to the inbox | `POST /api/v1/promotions/{id}/save` | `POST /api/bloem/v2/promotions/{id}/save` |
| Settings, Announcements | `/api/v1/admin/notifications/announcements` | `/api/bloem/v2/admin/platform/announcements`, in the platform context |
| Access groups and invitations outside an organization | `/api/v1/admin/access-groups`, `/api/v1/admin/invitations` | Silo's `/api/v2`, through Silo's own web code; outside an organization the invitations page shows Silo's invitations screen |
| Seasonal packs on signed-out pages | `GET /api/v1/theme/branding` (its `ambience` block) | `GET /api/bloem/v2/ambience/public`, public, the active deployment-wide packs (`web/src/components/seasonal/SeasonalLayer.tsx`) |

Silo's `/api/v2` home could not carry the campaign row: its dismissals accept only
`continue_watching` and `next_up`, and its home cards have no field for a campaign. Adding
those would mean changing Silo's code, so the campaign routes are Bloem's own. The new viewer
routes take the same checks as the native seasonal packs route (sign-in, organization, rate
limit, viewer and PIN check, a selected profile) and are advertised as
`promotions_viewer_v1`.

### Retired with `/api/v1` (2026-11-11)

Until 2026-11-11 these old routes stayed mounted, unchanged, for the callers below. Since
then they answer `410 Gone` like the rest of `/api/v1`.

| Route | Who still called it | Where it went |
| --- | --- | --- |
| `/api/v1/admin/tenants/...`, `/api/v1/admin/users/{id}/profiles`, `devices`, `auth-sessions` | Nobody | `/api/bloem/v2/admin/platform/...` |
| `/api/v1/admin/notifications/announcements`, `/api/v1/admin/promotions`, `/api/v1/admin/ambience` | Nobody | `/api/bloem/v2/admin/platform/...` |
| `GET /api/v1/promotions`, `POST /api/v1/promotions/{id}/save`, and Home's `promoted` row and `promo:` dismissals on `/api/v1/home` | Android, Apple | `/api/bloem/v2/promotions...`; the `promoted` row is also on Silo's `/api/v2/home` with `promoted=1` |
| The `ambience` block of `GET /api/v1/theme/branding` | Android, Apple (signed-out seasonal packs) | `GET /api/bloem/v2/ambience/public` |
| `GET /api/v1/ambience/assets/{ref}` | copies of old paths outside Bloem's tables, such as campaign cards already saved to an inbox | `/api/bloem/v2/ambience/assets/{ref}`, the only accepted prefix. Migrations `20261031234831_bloem_native_ambience_asset_paths` and `20261111215029_bloem_api_v2_ambience_asset_paths` rewrote the paths stored in seasonal packs and campaigns; copies kept elsewhere no longer load. |
| `/api/v1/admin/remote/...`, `/api/v1/profiles/household/sessions/{id}/commands`, `PUT /api/v1/devices/{id}/remote-control` | Android, Apple (the web app uses Silo's `/api/v2` session commands) | none: remote control is dormant ([admin remote control](admin-remote-control.md)) |
| `POST /api/v1/auth/profile-login`, `GET /api/v1/profiles/{id}` | Bloem's Apple app (profile sign-in) | none: profile sign-in is dormant ([security foundation](bloem-security-foundation.md)) |
| Bloem's fields on `/api/v1/notifications/capability` | the phone apps' remote-control discovery | none; `/api/bloem/v2/capabilities` gains them with the remote-control routes |
| `GET /api/v1/admin/host-stats` | Bloem's mobile admin apps | none; Silo's `GET /api/v2/admin/system/resources` reports the same measurements |
| `POST /api/v1/sync/progress` and Bloem's native `POST /sync/progress` | Android | Silo's `POST /api/v2/sync/progress`; Bloem's native duplicate and its `progress_sync_v1` token went too (`862d0fec2`) |

The dormant features had no native home on purpose: only the phone apps used them, and their
routes are built when the apps move, so a route nobody calls is not left to drift. Silo's own
apps had already left `/api/v1`: on 2026-10-31 `silo-android` called none of it and
`silo-apple` only its health probe.

### Sitting on top of Silo, not inside it

The native routes reuse Silo's code only through what Silo's own `/api/v2` uses, and change
no Silo file:

- Account create and update (`internal/api/handlers/bloem_platform_accounts.go`) call Silo's
  account service, `CreateAdminAccount`, `UpdateAdminAccount`, `GetAdminAccount` and
  `DeleteAdminAccount`, the same service behind `/api/v2/admin/users`. Bloem adds one step:
  putting the account on a plan revision, and deleting it again if that fails. Silo's
  `/api/v1` account handlers are not used.
- Tenants, members, account resources, announcements, campaigns (authoring and the viewer's
  cards), seasonal packs, and organization domains and branding use Bloem-owned handlers,
  mounted by `mountBloemPlatformStorefront` (`internal/api/router_bloem_storefront.go`).
- The web app reaches Silo's `/api/v2` through Silo's own web functions (`web/src/api/v2/`),
  and Bloem's API through `nativeApi`.
- `cmd/bloem-silo-switch-check` drives both servers through Silo's `/api/v2`, so the switch
  stayed checkable when Silo's image stopped serving `/api/v1`. Its scripts still wait on
  `/api/v1/ready`, Silo's operational probe, which the retirement does not cover.

### Checking

- `contracts/api/v2/route-inventory.json` and the [API catalogue](../bloem/api-catalogue.md)
  list every mounted route; a new Bloem route under `/api/v1` is a mistake.
- `internal/api/v1_route_surface_test.go` compares the `/api/v1` routes with golden lists
  that hold only Silo's tombstone and probes, so adding one fails a test.
- `make verify-no-v1` (`scripts/bloem/verify-no-v1.py`) fails when a line Bloem adds names
  `/api/v1` or `/api/bloem/v1`. Dated records, path-rewriting migrations and pages about the
  retirement itself, this one included, are listed in `contracts/bloem-v1-allow.txt`.
- In a client, search for `/api/v1/` outside its own API.

Technical pages:

- [Bloem's routes on Silo's `/api/v1`](../bloem/api/v1-routes.md): the retired routes,
  each marked with its replacement.
- [How Bloem's native API is organized](bloem-client-surface.md).
- [How apps connect](bloem-apps.md).
- [Messages and promotions](bloem-messages-and-promotions.md): campaigns, seasonal packs and
  announcements.

The pictures are SVG files under `images/leaving-silo-v1/`, drawn by
`scripts/bloem/draw-pictures.py`. Redraw them there, not by hand.
