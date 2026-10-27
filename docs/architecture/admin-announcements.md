# Server admin announcements

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current. This is a Bloem addition; Silo has no announcement authoring. The
> admin API (`GET`/`POST /api/v1/admin/notifications/announcements`,
> `DELETE …/{id}`) and the alert body rules in `internal/notifications/alert_body.go`
> came first, in `06285608c` (2026-09-01), as the S-1 alerts of the
> [client engagement spec](../specs/client-engagement.md#a-alert-notifications-s-1).
> The routes are mounted by `mountBloemLegacyAdminV1` in
> `internal/api/router_bloem_wiring.go`. This page and the web authoring screen
> (`web/src/pages/admin-settings/AnnouncementsAdminSettings.tsx`) were added in
> `f66a6ee3b` (2026-09-05). In `03f658ba9` (2026-09-23) the navigation and
> search entries moved into `web/src/lib/bloemAdminNavigation.ts`, so Silo's
> settings layout needs only a small, ledgered edit. Announcements arrive in the
> notification inbox that Silo owns ([notifications](notifications.md)); the
> inbox page `web/src/pages/Notifications.tsx` is a recorded seam (a Bloem edit
> to a Silo file, listed in `contracts/seams.txt`). "Garden" below means
> bloem-garden, a separate Bloem campaign-authoring service whose admin
> contracts are in the same spec.

The server web UI exposes announcement authoring at `/admin/settings/announcements` through the existing admin settings navigation and search. Garden is not required to publish a server announcement.

The page uses the existing admin-only `/api/v1/admin/notifications/announcements` API. It lists publication records, previews a new message before POST, and confirms withdrawal before DELETE. Publishing immediately resolves the audience and fans out to recipient inboxes; there is no draft persistence, scheduled start, or edit-published operation. Withdrawal retains the publication record.

Audiences are all viewers, account role, organization, library access, or explicit users/profiles. Explicit user targeting includes all profiles of each selected account. The current composer accepts IDs for organization, library, and explicit recipients. The backend remains authoritative for audience resolution and authorization.

Messages support information, warning, and critical severity, optional links and artwork, an action, and expiry. Critical messages cannot be dismissed by viewers. The preview shows the audience, expiry, dismissal policy, and supplied link addresses. Publishing errors preserve the draft. POST and DELETE use the API client's non-retrying transport policy.

The web notification inbox renders announcement titles, full messages, and severity using the existing read/unread controls. Read status is separate from banner dismissal. Basic server announcements do not depend on Garden. Rich campaign authoring, playback overlays, and seasonal effects can remain Garden features.

This authoring page does not implement viewer banners, player overlays, campaign rendering, or seasonal effects. Viewer clients must interpret the structured announcement body separately; authoring support alone does not establish end-to-end presentation support.

Focused validation:

```sh
pnpm --dir web test src/pages/admin-settings/AnnouncementsAdminSettings.test.tsx src/pages/admin-settings/AdminSettingsLayout.test.tsx
pnpm --dir web build
```
