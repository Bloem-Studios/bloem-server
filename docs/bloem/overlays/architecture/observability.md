# Bloem overlay: Observability

This overlay covers Silo's [`docs/architecture/observability.md`](../../../architecture/observability.md), which stays exactly as Silo wrote it.

Silo's page explains how the server reports on itself: logs, metrics, traces and
which database queries each request made. Bloem reports the same way. It adds one
note: the visitors of the storefront preview, whose requests are not written to the
request log.

## What Bloem changes

- **Server load for apps.** Bloem's administrator apps read the server's live CPU,
  memory and network use from Silo's `GET /api/v2/admin/system/resources`.
- **Preview visitors are not logged per request.** People looking around through
  the storefront's **preview** (a browse-only guest visit) do not get a line in the
  request log. Their database work still counts in the totals and metrics.

## Words used in the code

| On this page | In the code and API |
| --- | --- |
| Server-load reading | `GET /api/v2/admin/system/resources` |
| The measurements | Silo's `internal/nodemetrics` sampler |
| Preview visitors | preview guest requests (`internal/api/bloem_preview_activity.go`) |
| Not logged | `skipGuestLogging` |

## For developers

### Section by section

---

**Location:** section “Query attribution”, after the paragraph beginning “The
`api request`, `apiv2 request` and `jellycompat request` lines add, in order,…”.
**Bloem adds:**

Bloem's `/api/bloem/v2` routes share the main router, so they are logged on the
`api request` line with the same `db_*` fields. Preview-guest requests (on the
configured preview host, under `/api/bloem/v2/preview/`, or carrying a guest
credential) write no `api request` line (`skipGuestLogging` in
`internal/api/bloem_preview_activity.go`), so they carry no `db_*` request fields;
their queries still count in site totals, metrics, events (with `request` false)
and summaries, and guest `/api/v2` requests keep their `apiv2 request` line.
Bloem's packages share Silo's module path, so their call sites are named like
Silo's; they add pgx call lines, so Bloem's per-site memory bound is higher than
Silo's estimate.

### Related pages

- [Browse-only content policy](../../../architecture/bloem-browse-only-content-policy.md):
  the preview and its guest sessions.
- [How plans work](../../../architecture/bloem-plans.md): the storefront preview in
  plain language.
- [Writing Bloem documentation](../../documentation-workflow.md#overlays): how an
  overlay is written and kept up to date.
- Index: [Bloem documentation](../../README.md).
