# Task 8 report

Implemented organization-scoped security administration and committed it as the Task 8 change.

## Delivered

- Adapted Access Groups to the active Organization context using only context-qualified `/api/bloem/v1/admin/organization` queries and mutations, while preserving the existing Platform/v1 surface.
- Added the active organization identity, server-supplied member counts, authorization revision preconditions, organization library ceilings, and a safe deletion confirmation that previews profile reassignment to the same organization's default group.
- Adapted Invitations to list and create through Organization v2 resources, with organization identity and organization-scoped group/library choices. Unsupported v2 resend/revoke controls are not rendered, so the page cannot fall back to v1.
- Added Libraries & Entitlements with explicit owned-versus-Platform-entitled presentation and the effective-access intersection explanation.
- Added read-only organization Policy Decisions with subject, membership, access group, library ceiling, action/resource/outcome/reason and contributing policy versions; server-redacted values remain visibly redacted.
- Kept Rego management Platform-only and disabled its legacy capability query before returning the Organization-context denial.
- Wired all four Organization routes into the guarded admin shell.

## TDD and verification

- RED observed: new page modules were missing, and existing Access Groups/Invitations returned no v2 test data because they still called v1.
- GREEN: `NODE_OPTIONS=--no-experimental-webstorage pnpm vitest run src/pages/AdminAccessGroups.test.tsx src/hooks/queries/admin/accessGroups.test.ts src/pages/admin-organization src/pages/admin-settings/InvitationsTab.test.tsx src/pages/admin-policy` — 13 files, 31 tests passed.
- Focused ESLint over every touched TypeScript/TSX file — passed.
- Focused Prettier check over every touched TypeScript/TSX file — passed.
- `git diff --check` — passed.
- Full `pnpm exec tsc -b --pretty false` reaches one pre-existing unrelated error in `src/pages/admin-platform/OrganizationsPage.test.tsx`: unused `path` parameter. Task 8's `replaceAll` target-compatibility issue found by that run was corrected.

## Scope notes

- Organization invitation v2 currently supports list/create only, matching the backend contract; resend/revoke stay on the legacy Platform surface and are deliberately absent in Organization context.
- Entitlement mutations are not exposed from the Organization page. The view communicates the tenant ceiling without implying an unconditional grant.

## Review fix wave

- Organization invitation creation now strips the legacy `role` property and sends the exact additive v2 request body; a submit-level wire test protects the contract.
- Policy Decisions now follows `next_cursor` with an encoded cursor and appends later pages without dropping the visible history.
- Every library access switch now has an explicit accessible name, including an organization-qualified name for the all-libraries ceiling control.
- Review verification: 6 focused files / 9 tests passed; touched ESLint and Prettier passed; `git diff --check` passed. Full TypeScript remains blocked only by the unrelated pre-existing unused `path` in `OrganizationsPage.test.tsx`.
