# Bloem request policy

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current. The request service, its quotas and its request routing are Silo's
> (`internal/requests`; routing rules came with Silo #1632, `9d63465bf`; see
> Silo's [media requests](media-requests.md)). This page covers the Bloem
> adapters that bind them to Bloem's organizations (tenants) and verified
> profiles. Those adapters live in Bloem-owned files: `internal/requests/bloem_*.go`,
> `internal/api/bloem_request_usage_wiring.go` and `enableBloemRequestPolicy` in
> `internal/api/router_bloem_wiring.go`. The profile and quota binding was added in
> `f167e85be` (2026-10-10) and the platform guard on server-global routing in
> `1da67971e` (2026-10-10). Two later changes matter. `c60b7008e` (2026-10-26)
> moved an account's policy (its request override and access group) back onto
> `users`, where Silo keeps it, and limited an account to one organization.
> `b017bfe84` (2026-10-26) removed the lifecycle-receipt dependency. "Native
> listeners" below means the two routers that serve Silo's API: the `/api/v1`
> router and the `/api/v2` listener.

The movie and series request service remains shared with Silo. Bloem installs
owned tenant adapters before composing either native listener. Unsupported production dependencies
fail composition instead of silently retaining account-wide authorization.
Database-free composition and ordinary Silo services retain their existing
behavior. A DB-only discovery router has no authentication middleware or user
repository; it leaves request operations unavailable. Every authenticated
composition still requires the verified-profile policy dependencies.

## Viewer requests

Permission and group quota resolve from the request's verified access scope.
The account, organization, membership and profile must agree with that scope.
A single database snapshot checks the enabled account, active membership,
organization state, security and policy revisions, and profile ownership. It
then reads permission and quota from that profile's organization-owned group.
The account repository's default or oldest membership does not select a
profile's request policy.

Missing, foreign or unverified profiles fail closed. Profile-free compatibility
resolution is restricted to the actual default organization in the legacy
context, including its supported initializing state. An asserted default flag
cannot turn a hosted organization into that compatibility context.

The shared service still owns permission overrides, settings/group/user-limit
precedence, approval rules, quota consumption and request enrichment. Account
consumption remains shared across profiles; selecting a different profile does
not reset its counter. Services without the Bloem provider keep Silo's ordinary
account-group resolution.

## Administrative reads and group writes

`GET /api/v2/admin/request-users/{user_id}/usage` explicitly addresses an
account and supplies no target profile. Its separate adapter
(`internal/requests/bloem_admin_request_usage.go`) reports that account's own
policy, meaning the request override and access group on its `users` row, within
the actor's organization. It does not
infer a profile or relax the viewer-request resolver. One live snapshot checks
the enabled actor's current administrator membership and tenant revisions,
then the enabled target's active membership and organization-owned group.
Foreign, suspended and disabled targets are refused. Quota evaluation reuses
the shared settings, overrides and account-consumption calculation.

Administrative group-limit reads and conditional writes bind the group to the
verified request organization and actor. A write locks the group's ownership
through the revision-checked transaction. A preflight against another
organization's group cannot authorize its quota or approval policy. These
adapters retain the request repository's other optional capabilities.

## Global routing administration

Request routes and the routing mode are server-global. The existing tenant
service decorator requires account-admin authority in the actual default
organization before listing, reading, creating, updating, deleting, reordering
or previewing routes, and before reading or changing the routing mode. This
matches the existing boundary for global request settings and integrations.
The decorator preserves the interfaces consumed by both native listeners;
TMDB title search retains its existing behavior.

Ordinary organization-only administrators already fail the account-admin
transport gate. Current public account-session issuers do not issue an
account-admin token bound to a hosted tenant. The additional service check
also rejects that trusted bound-admin context, so accepting such a context in
the future cannot give it access to server-global routing configuration.
Denied writes leave route rows, priorities, revisions and routing mode intact.

Regression coverage includes conflicting account/profile groups, sibling
profile policies, membership and user-limit overrides, account consumption,
internal request enrichment, foreign groups, stale authority, production
listener composition, real v2 requests, and ordinary Silo compatibility.
