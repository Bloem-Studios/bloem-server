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
last_reviewed: 2026-08-21
related:
  - /admin/platform/entitlement-templates
---

# Entitlement Templates

Entitlement templates let a platform administrator define access policy once
and apply an exact, immutable revision to an organization or a directly managed
account.

Open **Admin → Platform → Entitlement templates**. Every template controls:

- all libraries or selected libraries;
- whether playback and transcoding are allowed;
- stream, profile, and transcode limits;
- original downloads and transcoded downloads independently;
- maximum playback quality;
- media requests; and
- allowed access-group permissions.

Vondel includes Browse-only, Viewer, Standard, Premium, and Reseller Member
starting points. They can be revised or cloned to fit an installation's own
plans.

## Safe workflow

1. Create or select a template.
2. Review its policy and enabled state.
3. Create a new revision for every change. Old revisions are never rewritten.
4. On the organization or direct-account page, select the exact revision and
   run the preview.
5. Review the changes, then confirm within ten minutes.

Rollback also creates a new revision from an older policy. Custom access groups
are preserved when an organization template is applied.

**All libraries** follows every library currently and subsequently available
to the target. Selected libraries are an allowlist. This is separate from an
organization's platform-library grants: the organization must still own or
hold a live grant for a library before a template can permit it.

For detailed operations, troubleshooting, and API automation, see
[Entitlement template operations](../../operations/entitlement-templates.md).

## Source References

- `internal/entitlements/template_store.go`
- `internal/api/handlers/entitlement_templates.go`
- `internal/api/router_v2.go`
- `web/src/pages/admin-platform/EntitlementTemplatesPage.tsx`
