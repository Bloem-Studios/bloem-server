# Internal ebook request admission

`bookrequests.CreateEbook`, `GetEbook` and `ActEbook` provide an internal
foundation for ebook requests. The public request routes, capabilities and
web flows continue to expose audiobooks only. This foundation has no
Bookwarehouse request router, acquisition worker, catalog reconciliation or
notification delivery.

Filing and approval require the exact unencrypted value `true` for
`bloem.requests.ebooks_enabled`; the admin setting defaults to `false`.
The caller's organization must be the durable default organization, with
an enabled account and active membership. An administrator cannot bypass
the hosted-organization restriction. Ordinary callers also need a current
profile belonging to their account and organization. `Viewer.Admin` remains
verified authority supplied by the trusted caller.

Filing also applies the existing request/group policy. Authorized reads,
cancellation and denial recheck identity and membership while remaining
available after acquisition opt-out. Approval leaves the request awaiting
fulfilment; internal manual completion is unavailable.

Ebooks use media type `book` and require a valid canonical ISBN-13, including
conversion from ISBN-10. ASIN and title-only identities are unavailable.
Identity indexes and account admission locks prevent concurrent duplicate
filings. Quotas, reads, actions and reconciliation are isolated by media type.
Audiobook reconciliation cannot complete an ebook with the same ISBN, and
the existing audiobook notification integration receives no ebook events.

Idempotency keys retain their organization/account/profile scope. Reusing a
key across media types returns a conflict; existing audiobook payload digests
and retries retain their previous meaning. The additive migration widens the
request table's media-type constraint and enforces ebook identity shape. Its
rollback refuses atomically while ebook rows exist, preserving request history.

Public ebook operations, capability and client adoption, kind-aware
notifications and fulfilment routing require separate implementation before
this foundation can be exposed to users.
