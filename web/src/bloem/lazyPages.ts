// Lazily loaded pages used by the Bloem route table (src/bloem/routes.tsx).
import { lazy } from "react";

export const LiveTVWatch = lazy(() => import("@/pages/LiveTVWatch"));
export const AdminAccessGroups = lazy(() => import("@/pages/AdminAccessGroups"));
export const AdminLiveTV = lazy(() => import("@/pages/AdminLiveTV"));
export const ActivityAuditPage = lazy(() => import("@/pages/admin-organization/ActivityAuditPage"));
export const EntitlementCohortsPage = lazy(
  () => import("@/pages/admin-organization/EntitlementCohortsPage"),
);
export const LibrariesEntitlementsPage = lazy(
  () => import("@/pages/admin-organization/LibrariesEntitlementsPage"),
);
export const OrganizationOverviewPage = lazy(
  () => import("@/pages/admin-organization/OrganizationOverviewPage"),
);
export const PeoplePage = lazy(() => import("@/pages/admin-organization/PeoplePage"));
export const PolicyDecisionsPage = lazy(
  () => import("@/pages/admin-organization/PolicyDecisionsPage"),
);
export const CompatibilityApplicationsPage = lazy(
  () => import("@/pages/admin-platform/CompatibilityApplicationsPage"),
);
export const DirectAccountPolicyBulkPage = lazy(
  () => import("@/pages/admin-platform/DirectAccountPolicyBulkPage"),
);
export const DirectAccountsPage = lazy(() => import("@/pages/admin-platform/DirectAccountsPage"));
export const EngagementPage = lazy(() => import("@/pages/admin-platform/EngagementPage"));
export const EntitlementTemplatesPage = lazy(
  () => import("@/pages/admin-platform/EntitlementTemplatesPage"),
);
export const OrganizationDetailPage = lazy(
  () => import("@/pages/admin-platform/OrganizationDetailPage"),
);
export const OrganizationsPage = lazy(() => import("@/pages/admin-platform/OrganizationsPage"));
export const InvitationsTab = lazy(() => import("@/pages/admin-settings/InvitationsTab"));
export const LiveTV = lazy(() => import("@/pages/LiveTV"));
