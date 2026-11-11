# Bloem overlay: Admin API

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`, and checked again on
> 2026-11-11, after Silo retired its v1 API. It lists what Bloem
> adds to Silo's [`docs/admin-api.md`](../../admin-api.md), which stays exactly as
> Silo wrote it. Bloem replaces nothing there. The section-by-section additions are
> in the [For developers](#for-developers) section.

Silo's Admin API page documents the routes an administrator uses to run a server.
Bloem serves all of them unchanged and adds three things of its own, after Silo's
section on reading the application log.

## What Bloem changes

- **Remote control of playing apps (dormant).** An administrator could pause, seek,
  stop or send a message to an app someone was watching on, but only an app that said
  it accepted that command. Its routes lived on Silo's v1 API, which Silo retired on
  2026-11-11, so the feature is parked until it has native routes.
- **Passwords and organizations.** Silo's password-reset links and temporary
  passwords work in Bloem too. They belong to the **account**, the sign-in, and do
  not change who owns an **organization** or who manages a household.
- **Storage sources (experimental).** Bloem's **native storage** lets ebook, movie
  and audiobook libraries read files from approved storage plugins, such as S3 or
  Bookwarehouse sources, beside ordinary folders. Platform and organization
  administrators manage those sources through their own routes, and Bloem refuses
  the library operations that cannot work on them yet.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Remote control (dormant) | `internal/remote`, `internal/api/handlers/remote_control.go`; its retired v1 routes are on [Bloem's retired v1 routes](../api/v1-routes.md#remote-control) |
| An app saying it accepts a command | the control socket's `hello.capabilities.commands` |
| Native storage | `/native-storage` routes in the platform and organization administrative scopes |
| Storage source on a library | `storage_source` on v2 `createLibrary`, or the native `library-locations` routes |

## For developers

### Where this overlay came from

Bloem first documented these surfaces in the Silo file in August and September 2026
and moved them here in `47ea7c889` (2026-09-23):

- admin remote control of playing clients, added in `e1efec046` (2026-09-02);
- how Silo's September account-password changes sit beside Bloem's organization
  membership, recorded with the September 30 integration;
- the experimental native storage administration routes, added from
  `ccc18d1b1` (2026-10-06); see
  [native storage](../../architecture/bloem-native-storage.md).

The overlay was reviewed on 2026-10-27 against Silo `3e2d306b2`, when Silo's
`8cbc82028` (#2781, 2026-10-26) had begun refusing plugin manifests whose ID or
version maps to an empty, `.` or `..` directory; that applies to Bloem unchanged.
From `3e2d306b2` through `63ef34b23` Silo changed `docs/admin-api.md` again in ten
commits, among them administrator playback history and session announcements
(#3184, #3191, #3194), monitoring and session-control API key scopes (#3182), the
per-session request quota and API clients card (#3075), egress by proxy node
(#3256), refusing administrator writes to the JWT signing keys (#3309) and starting
Silo without root (#3338). All apply to Bloem as written; none changes the anchor
section below. Silo's own sequenced administrator playback commands
(`/api/v2/admin/sessions/{session_id}/pause`, `/resume`, `/stop`, `/message` and
`/terminate`) were separate from Bloem's retired v1 remote-control routes.

### Section by section

**Location:** section “`GET /api/v2/admin/logs/app` — `level`”, after the paragraph
beginning “`level` accepts a comma-separated list, so one request can ask…”.
**Bloem adds:**

#### Remote control of playing clients (dormant)

Bloem's remote-control routes (v1 `/admin/remote/...`, `PUT /devices/{device_id}/remote-control`
and the household command route) lived on Silo's v1 API and answer `410 Gone` since Silo
retired it on 2026-11-11. Nothing is added to Silo's admin API for remote control until
native routes exist. The former contract is on
[Bloem's retired v1 routes](../api/v1-routes.md#remote-control), and the rules on
[how remote control works](../../architecture/admin-remote-control.md).

#### September 30 upstream integration

Upstream account password-reset links and temporary-password flows are described
in [password resets](../../architecture/password-resets.md). The setup owner flag
belongs to the account; it does not replace Bloem organization ownership or
primary-profile authority. Account policy lives on `users`, as in Silo, since
`c60b7008e` (2026-10-26); an organization membership only links the account to
its one organization. Bloem keeps its organization-scoped session revocation and
carries those Silo account fields through its own service integration. See
[adapter boundaries](../../architecture/bloem-upstream-adapters.md) and the
[deployment record](../../operations/2026-09-30-upstream-deployment.md).

#### Experimental native-storage administration

Bloem's platform and organization administrative scopes expose separate
`/native-storage` routes for approved and catalog artifact installation, S3 root
discovery, upgrade, sanitized source inspection and browsing, empty-namespace
configuration replacement, S3 credential rotation, stateless S3 bucket/folder
browsing, disable and uninstall. They use the existing signed administrative
context and retained current resource authority. A library attaches a source
through the ordinary v2 `createLibrary` (`storage_source`) or the scoped
`library-locations` routes. The [native API reference](../../bloem-api-reference.md#native-storage-administration)
lists methods, request fields, state revisions, error codes and recovery.

The protected capability document checks actual composition; it remains
separate from artifact approval and keeps `backend_verified: false`. Enable,
update/delete/unbind, scoped scan and repair are not supported native-library
operations. Ordinary installation/configuration/update/test/auth-binding/task-binding
routes reject explicitly marked native installation IDs; changing native source
state must use its authorized lifecycle routes.

Finite guards also cover selected ordinary v1/v2 library, scan, repair,
metadata, image, translation and trailer operations. They authorize the whole
selected set before native classification, preserving hidden-resource and PIN
refusal precedence. Authorized native targets return
`409 native_library_delete_unsupported`, `native_repair_unsupported` or
`native_local_operation_unsupported`; inconsistent classification returns
`503 native_storage_unavailable`. V2 preserves the native problem code.
Later selected mutation phases check fresh authority before acting; earlier
authorized commits remain committed. These checks do not claim a global lease
over unrelated local writes or implement native repair/conversion.

Native full scans reuse the durable host queue. Coalescing into a running run
records one follow-up that is enqueued on completion or failure, while a queued
accepted run needs no follow-up. Native execution uses the same cancellable
overlap waiting as ordinary ingestion. A `202` means the queue admitted the
request; it is not successful EPUB/PDF publication or backend acceptance.

### Related pages

- [Native storage](../../architecture/bloem-native-storage.md) and the
  [native API reference](../../bloem-api-reference.md#native-storage-administration).
- [Password resets](../../architecture/password-resets.md) and the
  [authentication overlay](auth-api.md).
- [Library diagnostics overlay](libraries-api.md): storage sources on libraries.
- [Writing Bloem documentation](../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../README.md).
