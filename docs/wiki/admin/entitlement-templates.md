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
last_reviewed: 2026-11-16
related:
  - /admin/platform/entitlement-templates
  - ../admin-guide.md
  - ../../architecture/bloem-browse-only-content-policy.md
---

# Entitlement templates

Entitlement templates (plans) are a Bloem addition; Silo's own access control is the
per-account policy and access groups described in the
[admin guide](../admin-guide.md#25-access-groups). This is the short guide for a platform
administrator. The technical rules are in the [For developers](#for-developers) section.

An **entitlement template** is a plan: access policy you define once and apply,
as one exact revision, to an organization or to a direct account. This page walks
through what a plan controls, the plans Bloem starts with, and the safe way to
change one. The plain explanation is
[how plans work](../../architecture/bloem-plans.md).

## What a plan controls

Choose the **Platform** administrative context, then open **Entitlement
Templates** in the Platform section of the admin sidebar
(`/admin/platform/entitlement-templates`). Every plan sets:

- all libraries, or a chosen set of libraries;
- whether playback and video conversion (transcoding) are allowed;
- how many screens, profiles and conversions at once;
- downloads of the original file and of a converted copy, each on its own;
- the best picture quality;
- whether members can make media requests; and
- which access-group permissions members get.

**All libraries** follows every library the organization or account can reach,
now and later. A chosen set is an allowlist. Either way, a plan can only permit a
library the organization already owns or holds a live grant for; a plan never
grants a platform library by itself.

## The plans Bloem starts with

![The five built-in plans compared: plays video, screens, conversions, quality, downloads and requests](../../architecture/images/plans/starting-plans.svg)

Bloem includes **Browse-only**, **Viewer**, **Standard**, **Premium** and
**Reseller Member** (keys `browse-only`, `viewer`, `standard`, `premium`,
`reseller-member`). Revise them or clone them to fit what you offer.
Browse-only can never be revised to allow playback.

A sixth plan, **Preview** (key `bloem-preview`), backs the storefront's
browse-only previews. It is protected: it cannot be switched off or loosened, and
no other plan can take its key. See
[browse-only previews](../admin-guide.md#browse-only-previews) and
[how browse-only access and the preview work](../../architecture/bloem-browse-only-content-policy.md).

## The safe way to change a plan

![Revisions 1 to 4 of a plan, accounts staying on revision 3, and four steps to move them](../../architecture/images/plans/revisions.svg)

1. Create or select a plan.
2. Check its policy and whether it is enabled.
3. Create a **new revision** for every change. Old revisions are never rewritten.
4. On the organization's page (**Plan**) or the **Direct Accounts**
   page, select the exact revision and run the preview.
5. Review the changes, then confirm within ten minutes. The confirmation belongs
   to what you previewed; if anything changed, preview again.

To roll back, create a new revision from an older one. Groups an organization
made itself are kept when you apply a plan to the organization.

To move a reviewed set of existing accounts between exact revisions, use policy
cohorts and bulk jobs: **Change Many Accounts** for direct accounts, **Policy
Cohorts** in an organization context. See
[how plans work](../../architecture/bloem-plans.md).

## Who can change what

| Who | What they manage |
| --- | --- |
| Platform administrator | Creating, revising, cloning and archiving plans; applying them to organizations and direct accounts. |
| Organization administrator | Moving their organization's people between revisions, under **People → Apply policy**. |
| The storefront (Bloem HQ) | Listing plans and putting one account on a plan, with a platform administrator's API key. |

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| Plan | entitlement template (`internal/entitlements`) |
| Revision | `entitlement_template_revisions` |
| All libraries | `library_ids: null` |
| Live grant | an organization library entitlement |
| Policy cohort, bulk job | entitlement cohorts and bulk policy jobs |

## For developers

- **API:** [plans and preview](../../bloem/api/plans.md) and the older
  [native API reference](../../bloem-api-reference.md#entitlement-template-administration).
- Plans, revisions and cohorts in plain language:
  [how plans work](../../architecture/bloem-plans.md).

Source references:

- `internal/entitlements/template_store.go`
- `internal/api/handlers/entitlement_templates.go`
- `internal/api/router_bloem.go`
- `migrations/sql/20260821170000_entitlement_templates.sql`,
  `migrations/sql/20261014120000_bloem_preview_template.sql`
- `web/src/pages/admin-platform/EntitlementTemplatesPage.tsx`,
  `web/src/pages/admin-platform/EntitlementTemplateEditor.tsx`
