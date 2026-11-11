import PreviewItemCampaigns from "./preview/PreviewItemCampaigns";
// Bloem routes and route wrappers, spliced into the Silo-owned App.tsx route
// table at single insertion points so App.tsx stays close to upstream.
import type { ComponentType, ReactElement, ReactNode } from "react";
import { Navigate, Route } from "react-router";
import { OrganizationContextGuard } from "@/contexts/AdminContextProvider";
import { AdminContextRedirect, AdminContextSelection } from "./BloemAdminShell";
import {
  AdminAccessGroups,
  LiveTVWatch,
  AdminLiveTV,
  ActivityAuditPage,
  EntitlementCohortsPage,
  LibrariesEntitlementsPage,
  OrganizationOverviewPage,
  PeoplePage,
  PolicyDecisionsPage,
  StorageSourcesPage,
  DirectAccountPolicyBulkPage,
  DirectAccountsPage,
  EngagementPage,
  EntitlementTemplatesPage,
  OrganizationDetailPage,
  OrganizationsPage,
  OrganizationInvitationsTab,
  LiveTV,
} from "./lazyPages";

/** Full-screen live TV playback, guarded by the App's profile gate. */
export function liveTVWatchRoute(RequireProfile: ComponentType<{ children: ReactNode }>) {
  return (
    <Route
      path="/watch/live/:channelId"
      element={
        <RequireProfile>
          <LiveTVWatch />
        </RequireProfile>
      }
    />
  );
}

/** Bloem viewer pages inside the profile-gated app shell. */
export const bloemViewerRoutes = <Route path="/livetv" caseSensitive element={<LiveTV />} />;

/** Platform-context admin pages, mounted inside the platform context guard. */
export const bloemPlatformAdminRoutes = (
  <>
    <Route path="platform/organizations" element={<OrganizationsPage />} />
    <Route path="platform/organizations/:id" element={<OrganizationDetailPage />} />
    <Route path="platform/entitlement-templates" element={<EntitlementTemplatesPage />} />
    <Route path="platform/engagement" element={<EngagementPage />} />
    <Route path="platform/direct-accounts" element={<DirectAccountsPage />} />
    <Route path="platform/direct-accounts/bulk" element={<DirectAccountPolicyBulkPage />} />
    <Route path="livetv" element={<AdminLiveTV />} />
  </>
);

/** Organization-context admin pages plus the context chooser and fallback redirect. */
export const bloemAdminContextRoutes = (
  <>
    {/* Nested under one "organization" route: React Router 8 ranks a sibling
        "organization/*" above "organization", which hid the overview. */}
    <Route element={<OrganizationContextGuard />}>
      <Route path="organization">
        <Route index element={<OrganizationOverviewPage />} />
        <Route path="people" element={<PeoplePage />} />
        <Route path="policy-cohorts" element={<EntitlementCohortsPage />} />
        <Route path="access-groups" element={<AdminAccessGroups />} />
        <Route path="libraries" element={<LibrariesEntitlementsPage />} />
        <Route
          path="invitations"
          element={
            <div className="page-shell py-4 sm:py-6">
              <OrganizationInvitationsTab />
            </div>
          }
        />
        <Route path="policy-decisions" element={<PolicyDecisionsPage />} />
        <Route path="storage" element={<StorageSourcesPage />} />
        <Route path="activity" element={<ActivityAuditPage />} />
        <Route path="*" element={<Navigate to="/admin/organization" replace />} />
      </Route>
    </Route>
    <Route path="context" element={<AdminContextSelection />} />
    <Route path="*" element={<AdminContextRedirect />} />
  </>
);

/**
 * Profile settings. The direct-account profile credentials panel is dormant:
 * profile sign-in lived only on Silo's retired /api/v1, so the panel stays
 * hidden until a native profile sign-in exists
 * (docs/architecture/bloem-security-foundation.md).
 */
export function withDirectProfileCredentials(settings: ReactElement) {
  return settings;
}

/** Item detail wrapped with engagement campaign placements. */
export function withItemCampaigns(detail: ReactElement) {
  return <PreviewItemCampaigns>{detail}</PreviewItemCampaigns>;
}
