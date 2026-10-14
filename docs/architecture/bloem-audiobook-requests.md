# Bloem audiobook requests

Bloem owns the audiobook request lane in `internal/bookrequests` and
`bloem_book_requests`. Silo's TMDB request store, routes and pages retain their
existing behavior. The web application wraps those pages at the existing
`/requests` and `/admin/requests` entry points and selects the audiobook lane
with `kind=audiobook`.

Approved requests may use the opt-in `bloem.requests.audiobooks` acquisition
backend. An enabled audiobook-only request integration and a valid library
binding with dedicated download roots make acquisition available. Without that
binding, existing catalog matching and manual fulfilment retain their behavior.
Ebook requests stay in their separate Bookwarehouse lane.

## Authority and policy

Filing requires an authenticated account, a verified profile, an active
organization membership, and a profile belonging to that organization and
account. Hosted organizations may file. The mirrored legacy default
organization may remain `initializing`; that exception does not admit an
initializing hosted organization.

The filing policy reads the profile's actual organization group and membership.
Membership `requests_allowed` overrides the group gate. Account request-limit
and approval overrides take precedence over group settings, then global request
settings. Organization administrators retain the existing exemption from group
policy; an explicit membership/account restriction still applies. Counters are
independent of Silo's movie/series and ebook lanes, and shared by an
account's audiobook requests across its profiles and organizations. Withdrawal consumes quota; decline
refunds it.

Ordinary reads and cancellation filter by organization, account and profile.
Sibling profiles do not see each other's requests. Administration uses a current
organization administrative context and filters before pagination. A platform
context alone grants no organization queue access. Requester-supplied headers,
query parameters and JSON fields cannot select another organization.

Verified direct-profile sessions may use only the explicit viewer routes. They
cannot use the administrative request routes. Jellyfin and Audiobookshelf do
not expose this Bloem-native request API.

## Identity and concurrency

Create requires an `Idempotency-Key` of 16–128 characters without whitespace.
The key is scoped to organization, account and profile. The stored digest covers
the normalized title, author, identifiers and note. An identical replay returns
the original request with HTTP 200, including after withdrawal or completion;
changed input for the same key returns 409. New filings return 201.

ISBN-10 converts to ISBN-13 and ASIN is uppercased. A title plus author may be
filed without an identifier. Active ISBN, ASIN and fallback title/author identity
indexes prevent concurrent duplicates within one organization, without exposing
another profile's request. Advisory-lock namespace 143 serializes filings by
account so independent book quota checks and retries cannot over-admit.

Mutations lock the request row and require `expected_revision`. Approval and
decline apply to pending requests; withdrawal and manual fulfilment apply to
pending or approved requests. Terminal outcomes and stale revisions return 409.
Manual fulfilment records `fulfilment=manual` and leaves `library_content_id`
empty; it does not claim that a playable audiobook was found.

## Catalog completion and notifications

The hidden `bloem_reconcile_book_requests` task runs every five minutes and
checks up to 100 rows per pass. Each request is locked with
`FOR UPDATE SKIP LOCKED`, allowing multiple API replicas to reconcile safely.
Either a pending or approved request can complete when its audiobook already
exists in the organization's catalog.

Completion requires all supplied stable identifiers to match one
`media_items.type=audiobook` item. ISBN-10 and ISBN-13 are equivalent. A title or
author resemblance is never sufficient, and an ebook with the same ISBN does
not qualify. The match also requires a media-file row in an enabled audiobook
folder owned by the organization, or in a platform folder with an active
entitlement for that organization. The request, provider-ID, catalog, file,
folder, owner, organization and entitlement rows are locked while completion
commits. This establishes catalog presence; it does not preflight filesystem or
storage-provider liveness. The ordinary scanner currently supplies audiobook
ASIN provider IDs; ISBN completion requires an ISBN provider ID in the catalog.

Submitted notices go only to current organization administrators. Approval,
decline and completion go only to the current requester. Recipients are
resolved again from enabled accounts, current profiles, memberships and
organization status before dispatch. Tenant request titles never go to the
deployment-wide server-channel announcement path.

Book lifecycle and manual-completion notices use the existing `system.alert`
operational delivery with stable request/event/account/profile IDs. Their links
open the audiobook request lane. Library completion uses the existing
`request.fulfilled` delivery and checks the recipient's current catalog access;
it cannot disclose an item the profile may not open. Notification master
preferences and existing per-account device/webhook filtering still apply.
Lifecycle notices are best effort on a bounded detached context. Completion
notices retry durably until the delivery succeeds, is deduplicated, or the
recipient is no longer eligible.

## Acquisition and review

The host registers `bloem_reconcile_book_requests` before starting the task
manager. Each pass independently reconciles ordinary library completion,
audiobook acquisition and ebook acquisition, so one lane's failure does not
starve the others. Audiobook acquisition checks at most 100 requests and starts
at most five searches within a three-minute run budget.

A dispatch freezes the integration ID, installation, capability and owner as
its identity. Renaming, credential/configuration edits, plugin version changes
and library/root rebinding do not replace an in-flight acquisition. The original
library, roots and language remain frozen for completion. Removing/replacing the
backend identity fails the acquisition; verified provider completion can still
match the retained library roots.

PostgreSQL leases serialize provider calls. A started marker commits before
Fulfill; ambiguous outcomes use a durable unknown receipt and read-only
CheckStatus, never a second submission. Definite host pre-RPC failures retain no
started marker. Cancel/refile chains share their original submission ID and
attempt, including across ISBN/ASIN identifier variants. Cancellation does not
cancel the external download. Retry keeps any in-flight lease: late receipts
and decisions cannot affect a newer attempt.

`request_router.v1` carries requester-free descriptors, an `abr1.` receipt and
an `abd1.` decision envelope. Both envelopes are strict, bounded and validated.
Secrets travel only in the ordinary encrypted request-integration API key field
as an `abk1.` composite, bound to normalized destination URL fingerprints. They
never enter the dispatch descriptor, receipt, decision log or requester view.
The plugin owns release search, AI selection/verification and downloader calls;
the host owns policy, identity, attempts and catalog completion.

The plugin answers `CheckStatus` in the host's vocabulary. `review` must carry a
verify decision with outcome `review` and holds the request for an
administrator; `completed` must carry external status `verified` and a
`verified` decision. `unknown` with external status `missing` (a confirmed
download the client no longer shows) is an uncertain check: it backs off and,
after five, reports the download lost. `unknown` with `absent` (a hand-off the
client never showed) is searched again after two absent checks. `unknown` with
external status `unknown` means the plugin could not read the download client.
That check keeps the request's state and counters, so a client outage never
reports a download lost; an outage longer than an hour shows as `unreachable` in
the admin stuck view. A failed `CheckStatus` call (the plugin itself
unavailable) is still an uncertain check. Pre-release plugin builds answered
`queued` for all four cases; the adapter maps those by external status to the
same states. The cross-process acceptance in
`internal/api/bloem_audiobook_lifecycle_test.go` runs this contract against the
real plugin executable when `BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY` is set.

No-match searches wait six hours and stop after seven days; unavailable sources
wait thirty minutes. Uncertain checks back off and stop after five failures.
Selections or downloads needing review wait for an administrator. An approved
candidate starts a fresh attempt; accepting a download permits catalog matching
without marking the request fulfilled. Manual fulfilment remains available.
Review/failure notices go only to current organization administrators and use
stable attempt-scoped delivery identities. The admin view retains the newest
50 validated decisions; requesters receive only safe progress and reason text.

Provider verification moves a request to `awaiting_library`. Completion still
requires a catalog item: either all supplied stable IDs match, or exactly one
nonmissing audiobook item exists below the retained dedicated download roots in
an attempt-marker folder. Two matching items require review. The path lookup
first materializes the library/path index range; it does not search the entire
catalog. A completed download does not create a scanner schedule or guarantee
filesystem liveness.

## Native client follow-up

The server advertises `audiobook_requests_v1` only when the viewer routes and
their authority middleware are mounted. The generated Bloem OpenAPI and client
DTOs describe the new lane. Existing native movie/series request flows remain
valid, but native audiobook UI integration is pending in both client repos:

| Repository | Existing owner | Required integration |
| --- | --- | --- |
| `bloem-apple` | `MediaRequestRepository` and `AppleRequestsScreen` | Add a separate capability-gated native book repository and form/history section; preserve the existing TMDB repository. |
| `bloem-android` | Network `SocialApi`, engine `RequestSurfaces`, mobile `TouchRequests`, TV `RemoteRequests` | Add separate native book operations and capability-gated form/history surfaces; preserve movie/series discovery. |

Both integrations must retain a filing key across transport retries, bind work
to the originating account/profile/organization, send `expected_revision` for
withdrawal, distinguish approval from fulfilment, handle the audiobook request
notification link, and clear rows/forms/pagination when authority changes.
They must not expose ebook fulfilment through this audiobook capability. Native
acquisition setup and review surfaces remain follow-up work for both clients;
the server exposes those operations through the Bloem admin API.
