# Bloem overlay: Admin API

This overlay covers Silo's [`docs/admin-api.md`](../../admin-api.md), which stays exactly as Silo wrote it.

Silo's Admin API page documents the routes an administrator uses to run a server.
Bloem serves all of them unchanged and adds three things of its own, after Silo's
section on reading the application log, and one note on how a transcode node reads
files in Bloem's native storage.

## What Bloem changes

- **Remote control of playing apps (dormant).** An administrator pausing, seeking,
  stopping or sending a message to an app someone is watching on needs routes that
  Bloem does not have yet, so the feature is parked until it has native routes.
- **Passwords and organizations.** Silo's password-reset links and temporary
  passwords work in Bloem too. They belong to the **account**, the sign-in, and do
  not change who owns an **organization** or who manages a household.
- **Storage sources (experimental).** Bloem's **native storage** lets ebook, movie
  and audiobook libraries read files from approved storage plugins, such as S3 or
  Bookwarehouse sources, beside ordinary folders. Platform and organization
  administrators manage those sources through their own routes, and Bloem refuses
  the library operations that cannot work on them yet.
- **Native-storage files on transcode nodes.** Silo's transcode nodes use no
  database or Redis, so a file kept in native storage reaches a node another way. The
  [native video playback](../../architecture/bloem-native-video-playback.md) page
  describes how such a file reaches a node.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Remote control (dormant) | `internal/remote`, `internal/api/handlers/remote_control.go`; the rules are in [how remote control works](../../architecture/admin-remote-control.md) |
| An app saying it accepts a command | the control socket's `hello.capabilities.commands` |
| Native storage | `/native-storage` routes in the platform and organization administrative scopes |
| Storage source on a library | `storage_source` on v2 `createLibrary`, or the native `library-locations` routes |

## For developers

### Section by section

**Location:** section “Node source access (v2)”, after its list of the two modes.
**Bloem adds:**

#### Native-storage files on a transcode node (changed)

Silo's two modes describe files in library folders. A file in Bloem's
[native storage](../../architecture/bloem-native-storage.md) is not in a library
folder: it is read through its storage source. A thin transcode node has no Redis,
so it takes no worker grant kept there. How a native-storage file reaches a
transcode node is on
[native video playback](../../architecture/bloem-native-video-playback.md).

---

**Location:** section “`GET /api/v2/admin/logs/app` — `level`”, after the paragraph
beginning “`level` accepts a comma-separated list, so one request can ask…”.
**Bloem adds:**

#### Remote control of playing clients (dormant)

Bloem mounts no remote-control routes. Nothing is added to Silo's admin API for remote
control until native routes exist. The rules are on
[how remote control works](../../architecture/admin-remote-control.md).

#### Passwords and account policy

Upstream account password-reset links and temporary-password flows are described
in [password resets](../../architecture/password-resets.md). The setup owner flag
belongs to the account; it does not replace Bloem organization ownership or
primary-profile authority. Account policy lives on `users`, as in Silo, and an organization membership only links the account to
its one organization. Bloem keeps its organization-scoped session revocation and
carries those Silo account fields through its own service integration. See
[adapter boundaries](../../architecture/bloem-upstream-adapters.md).

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

Finite guards also cover selected ordinary `/api/v2` library, scan, repair,
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
