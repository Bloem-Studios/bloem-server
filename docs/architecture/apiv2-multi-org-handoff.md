# API v2 multi-organization handoff

Branch `codex/apiv2-multi-org` contains the gentle integration work based on upstream
`apiv2`. The implementation keeps Silo's existing v2 architecture and adds organization
ownership, private libraries with platform-library grants, organization-scoped groups and
invitations, and access checks at request, notification, playback, and media-delivery
boundaries.

Requests use separate organization queues by default. Administrators can enable the optional
global request mode through the v2 admin request-settings endpoint. Global mode shares the
duplicate check and queue while preserving requester privacy in community notifications;
personal delivery and backend attribution retain the requester internally. Switching modes is
serialized and rejects conflicting active requests atomically.

The main commit is `545d90626`. Focused Go tests, API contract verification, migration
validation, frontend tests, TypeScript checking, frontend build, and local-path checks passed
when the branch was prepared. The branch has been pushed to `origin/codex/apiv2-multi-org`.

Remaining work is integration review: audit any backend routes or hosted inbox surfaces not yet
covered by organization scope checks, then decide whether to open a pull request. Do not push
to upstream Silo or deploy from this branch without explicit authorization.
