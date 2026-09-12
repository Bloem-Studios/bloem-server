# API v2 multi-organization handoff

Branch `codex/apiv2-multi-org` contains the gentle integration work rebased onto upstream
`apiv2` at `8eeb9f3e6`. The implementation keeps Silo's existing v2 architecture and adds organization
ownership, private libraries with platform-library grants, organization-scoped groups and
invitations, and access checks at request, notification, playback, and media-delivery
boundaries.

Requests use separate organization queues by default. Administrators can enable the optional
global request mode through the v2 admin request-settings endpoint. Global mode shares the
duplicate check and queue while preserving requester privacy in community notifications;
personal delivery and backend attribution retain the requester internally. Switching modes is
serialized and rejects conflicting active requests atomically.

The main commit is `fbfaa5292`. The branch adds roughly 920 production lines across 41 files;
the OpenAPI delta is two additive `global_requests` fields.

After the rebase: `go build ./...`, the v2 OpenAPI, web-type and fixture gates, the migration
ledger, `goose validate`, a fresh `goose up`, `golangci-lint --new-from-merge-base`, the
frontend TypeScript check and the admin-request frontend test all pass. Focused Go tests pass
for every touched package except two failures that reproduce identically on a clean
`upstream/apiv2` tree and are therefore not caused by this work:
`TestAdminSubtitleListPageDB` and `TestCatalogPersonalCursorDB`.

Two checks did not run: `verify-apiv2-contract` reports no base document to compare, and
`verify-local-paths` crashes under bash 3.2 on macOS while passing on the Linux CI runner.

Remaining work is integration review: audit any backend routes or hosted inbox surfaces not yet
covered by organization scope checks, then decide whether to open a pull request. Do not push
to upstream Silo or deploy from this branch without explicit authorization.
