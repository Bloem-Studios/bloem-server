# Bloem ebook requests

Ebook requests use `internal/bookrequests` and the Bloem-owned
`bloem_book_requests` table with media type `book`. The existing Requests page
and organization admin queue select this lane with `kind=ebook`. Audiobooks
retain their own routes, quota counters, retry semantics and manual completion;
Silo's TMDB request table remains separate.

## Admission and authority

Filing and approval require the exact unencrypted value `true` for
`bloem.requests.ebooks_enabled`; the default is `false`. Global request policy
must also permit the operation. The caller needs an enabled account and active
membership in the durable default organization. Ordinary viewers need a
verified current profile belonging to that account and organization. Hosted
organizations cannot file, including their administrators.

Authorized history, denial and local cancellation remain available after
opt-out. Mutations require the current request revision. Withdrawal retains
quota usage; denial refunds it. Quotas inherit existing request/group policy
but count ebooks independently from audiobooks and movie/series requests.

ISBN is mandatory. Valid ISBN-10 converts to canonical ISBN-13; ASIN and
title-only identities are unavailable. Filing uses a durable Idempotency-Key
scoped to organization, account and profile. Identical normalized retries
return the retained request; changed payloads or cross-kind key reuse conflict.
Existing audiobook digests remain valid.

## Fulfillment boundary

Acquisition uses a separate ordinary plugin, `bloem.requests.bookwarehouse`,
through the existing SDK `request_router.v1` interface. Native storage plugins
cannot declare this capability. Connections reuse the encrypted
`request_integrations` store and must explicitly advertise `book`; an empty
media-type list does not qualify. Integration IDs are opaque strings.

Platform administrators configure the lane from an administrative context for
the default organization. Policy, connection creation and library binding are
separate mutations. Connections default to EPUB and HTTPS; trusted local HTTP
requires explicit opt-in. PDF requires an explicit connection configuration
opt-in. Responses never contain connection credentials.

Each connection must be linked to an enabled native Bookwarehouse ebook library
accessible to the default organization. Approved requests remain awaiting
fulfillment when no eligible bound connection exists. Dispatch freezes a
secret-free descriptor containing the connection, installation, configuration,
binding and storage-source revision. Changes invalidate its use rather than
silently redirecting existing provider work to another library or account.

## Recovery and concurrency

The hidden book reconciliation task runs every five minutes with bounded
batches. PostgreSQL leases permit multiple server replicas. Provider RPCs run
outside database transactions, after persisting a dispatch-start marker.
Policy epochs fence responses across transient disable/enable changes; narrow
observers accept ordinary upstream settings writes while advancing only
Bloem-owned state.

Bookwarehouse lacks atomic idempotency. After an uncertain submission or process
loss, recovery and status calls only read provider state. They never blindly
repeat the acquisition POST. Retained receipts bind the canonical ISBN, format
and original submission identity. Canceling and refiling the same ISBN retains
that submission identity and recovers previous work before any new submission.
Unresolved state remains visible for admin review and retry. A host failure
before RPC entry, or the plugin's `FailedPrecondition`/`InvalidArgument` rejection
before submission, proves that nothing was sent and clears only a new,
receipt-free started claim. Generic transport errors remain uncertain and
recover only through reads. The library loader carries the storage location's
selection revision into its authority check.

Cancellation affects Bloem only. Bookwarehouse may continue work already
started. Disabling acquisition blocks new provider work; it does not promise
provider cancellation. Provider errors exposed to clients exclude credentials
and raw backend responses.

## Completion and notifications

Provider completion means awaiting a library scan. Fulfillment requires an
ISBN-matching `media_items.type=ebook` item with an `ebook` ISBN provider ID
in the retained linked library, a `probe_source=storage` EPUB/PDF file
reference, canonical catalog location and the retained storage-source identity.
A filesystem replacement, missing file, foreign library, audiobook or title
match cannot complete the request. Manual ebook fulfillment is unavailable.
Catalog completion does not claim provider liveness or a successful reader open.

Submitted notices go to current organization administrators; approval, decline
and completion go to the current requester. Kind-aware lifecycle notices link
to the ebook lane. Library-completion delivery checks current catalog access
and retries durably. No tenant book titles go to deployment-wide broadcasts.

## Client scope

`ebook_requests_v1` advertises the mounted API, while its per-profile capability
reports opt-in and current policy. This increment supports embedded web/admin
flows. Ebook request adoption in Bloem Apple and Android is deferred; ebook
routes are not added to the direct-profile session allowlist. Jellyfin and
Audiobookshelf do not expose this Bloem request API. Automatic audiobook
acquisition remains deferred.
