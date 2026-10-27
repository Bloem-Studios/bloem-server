---
title: Entitlement Templates
description: Reusable, revisioned access policy for organizations and direct accounts.
summary: Create, review, and safely apply playback, profile, transcode, download, permission, and library policy.
tags:
  - admin
  - access
  - entitlements
  - organizations
audience:
  - operator
last_reviewed: 2026-10-27
related:
  - /admin/platform/entitlement-templates
  - ../admin-guide.md
  - ../../operations/entitlement-templates.md
  - ../../architecture/bloem-browse-only-content-policy.md
---

# Entitlement Templates

> **Provenance.** Entitlement templates are a Bloem addition; Silo has no equivalent.
> Silo's own access control is the per-account policy and access groups described in
> the [admin guide](../admin-guide.md#25-access-groups). Bloem added templates in
> `3ec1650d7` (2026-08-21, migration `20260821170000_entitlement_templates.sql`) so a
> platform administrator could give many organizations and accounts the same policy
> without editing each one, and this page in `dbe8323d7` the same day. The protected
> **Preview** template followed in `25062b4df` (2026-10-14).
> Reviewed on 2026-10-27 against Bloem `main` at `e852f92bf`: the pages and stores
> listed under Source References. Source review only.

Entitlement templates let a platform administrator define access policy once
and apply an exact, immutable revision to an organization or a directly managed
account.

Choose the **Platform** administrative context, then open **Entitlement Templates**
in the Platform section of the admin sidebar (`/admin/platform/entitlement-templates`).
Every template controls:

- all libraries or selected libraries;
- whether playback and transcoding are allowed;
- stream, profile, and transcode limits;
- original downloads and transcoded downloads independently;
- maximum playback quality;
- media requests; and
- allowed access-group permissions.

Bloem includes Browse-only, Viewer, Standard, Premium, and Reseller Member
starting points (keys `browse-only`, `viewer`, `standard`, `premium`,
`reseller-member`). They can be revised or cloned to fit an installation's own
plans. Browse-only cannot be revised to allow playback.

A sixth built-in, **Preview** (key `bloem-preview`), backs browse-only guest
previews. It is protected: it cannot be disabled or loosened, and no other template
can take its key. See [browse-only previews](../admin-guide.md#browse-only-previews)
and the [security contract](../../architecture/bloem-browse-only-content-policy.md).

## Safe workflow

1. Create or select a template.
2. Review its policy and enabled state.
3. Create a new revision for every change. Old revisions are never rewritten.
4. On the organization or direct-account page, select the exact revision and
   run the preview.
5. Review the changes, then confirm within ten minutes. The confirmation is bound
   to the previewed state; if anything changed, preview again.

Rollback also creates a new revision from an older policy. Custom access groups
are preserved when an organization template is applied.

**All libraries** follows every library currently and subsequently available
to the target. Selected libraries are an allowlist. This is separate from an
organization's platform-library grants: the organization must still own or
hold a live grant for a library before a template can permit it.

To move a reviewed set of existing accounts between exact revisions, use policy
cohorts and bulk jobs (**Bulk Account Policies** for direct accounts, **Policy
Cohorts** in an organization context); see
[bulk policy cohorts](../../operations/bulk-policy-cohorts.md).

For detailed operations, troubleshooting, and API automation, see
[Entitlement template operations](../../operations/entitlement-templates.md) and the
[native API reference](../../bloem-api-reference.md#entitlement-template-administration).

## Source References

- `internal/entitlements/template_store.go`
- `internal/api/handlers/entitlement_templates.go`
- `internal/api/router_bloem.go`
- `migrations/sql/20260821170000_entitlement_templates.sql`,
  `migrations/sql/20261014120000_bloem_preview_template.sql`
- `web/src/pages/admin-platform/EntitlementTemplatesPage.tsx`,
  `web/src/pages/admin-platform/EntitlementTemplateEditor.tsx`
