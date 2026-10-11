# Bloem audiobook requests

Bloem owns the audiobook request lane in `internal/bookrequests` and
`bloem_book_requests`. Silo's TMDB request store, routes and pages retain their
existing behavior. The web application wraps those pages at the existing
`/requests` and `/admin/requests` entry points and selects the audiobook lane
with `kind=audiobook`.

This lane records requests and approval. It has no fulfilment integration.
An approved request stays `approved` with `fulfilment=awaiting_fulfilment` until
the catalog supplies a matching audiobook or an organization administrator
marks it fulfilled. There is no downloader, remote-source shortcut, or
Bookwarehouse connection behind approval. Ebook requests are not enabled by
this lane or its capability.

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
They must not expose ebook fulfilment through this audiobook capability.
