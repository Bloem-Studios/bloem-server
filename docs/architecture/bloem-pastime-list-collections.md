# Pastime list collections

The native Bloem list API is an owned extension under
`/api/bloem/v1/list-collections`. Its capability document lists only the current
profile's configured, active Pastime connections. Source origins are public host
configuration (`BLOEM_PASTIME_LIST_ORIGIN`); plugin provision credentials remain in
the protected plugin settings file.

A resolved URL is a short-lived preview of one stable source identity. Creation
chooses a personal, shared personal or administrator server audience explicitly.
Server mutations require the account's primary profile, following Silo's acting
administrator rule.
The creating profile remains the request actor for every audience. Source lists
control membership and order; native names, artwork and sharing remain local.
Ordinary collection readers and unlinked writers retain Silo behavior. A linked
collection must be disconnected before membership or source definition changes.

Snapshots contain only media kind, display metadata and exact provider identities.
They are bounded to 10,000 entries, 100 entries per page, 8 MiB and a 120-second
source lease. Complete publication verifies contiguous pages, total, digest,
generation, current authority and an Ed25519 source proof bound to the instance,
account, profile, stable source, snapshot and expiry. The host pins the source's
public signing key; private signing seeds stay encrypted in Pastime. Catalog
matching never guesses by title or crosses ebook and audiobook formats.

Membership, native revision, success receipt and missing-title request intents
commit atomically. Every accepted snapshot rematches the catalog, including when
its digest is unchanged. Refreshes run daily and may be queued manually. Failed
or incomplete snapshots retain the last successful membership. A valid empty
snapshot clears it. Installation admission limits active source jobs to three
across replicas.

Request delivery uses the existing native movie/series and book request services,
including their authority, maturity, quota and routing rules. Replay identities
are scoped to instance, account, profile, media kind and native provider identity;
source labels and list names cannot create another request. ISBN-10 and ISBN-13
representations normalize to the native edition. Ebooks require ISBN; an
ASIN-only ebook or video identity without a native TMDB mapping reports unsupported.
Availability does not imply ownership. Collection refreshes never change personal
tracking, copies or book progress.

An installation advisory lane precedes managed and native authority locks.
Publication and disconnect hold the lane transactionally. Dispatch holds a
session lane, commits row-locking checks before native service calls, and rechecks
source, target and wanted generation after receipt lookup. Delivery calls are
bounded to 90 seconds and leases to 120 seconds. A failed session unlock destroys
the pooled connection. Durable native receipt lookup recovers successful filings
after a lost response without charging quota again.

Disconnect restores manual editing and retains membership. It cancels unsent
intents; issued requests remain unchanged. Local owner authority permits this
operation even when the source is disabled. Bounded cleanup tombstones missing
targets and restores orphan native markers after grant or journal deletion.

Android and Apple clients detect the dedicated capability endpoint and use the
generated request and response types. The initial interactive workflow is the
web client. Native collection membership is visible through existing compatible
read projections; the source management API is Bloem-specific.
