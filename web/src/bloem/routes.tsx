// Bloem routes and route wrappers, spliced into the Silo-owned App.tsx route
// table at single insertion points so App.tsx stays close to upstream.
import { type ComponentType, type ReactElement, type ReactNode } from "react";
import { Navigate, Route } from "react-router";
import { ItemCampaigns } from "@/components/engagement/CampaignPlacements";
import { DirectProfileCredentials } from "@/components/profiles/DirectProfileCredentials";
import { OrganizationContextGuard } from "@/contexts/AdminContextProvider";
import AdminLiveTV from "@/pages/AdminLiveTV";
import ActivityAuditPage from "@/pages/admin-organization/ActivityAuditPage";
import EntitlementCohortsPage from "@/pages/admin-organization/EntitlementCohortsPage";
import LibrariesEntitlementsPage from "@/pages/admin-organization/LibrariesEntitlementsPage";
import OrganizationOverviewPage from "@/pages/admin-organization/OrganizationOverviewPage";
import PeoplePage from "@/pages/admin-organization/PeoplePage";
import PolicyDecisionsPage from "@/pages/admin-organization/PolicyDecisionsPage";
import CompatibilityApplicationsPage from "@/pages/admin-platform/CompatibilityApplicationsPage";
import DirectAccountPolicyBulkPage from "@/pages/admin-platform/DirectAccountPolicyBulkPage";
import DirectAccountsPage from "@/pages/admin-platform/DirectAccountsPage";
import EngagementPage from "@/pages/admin-platform/EngagementPage";
import EntitlementTemplatesPage from "@/pages/admin-platform/EntitlementTemplatesPage";
import OrganizationDetailPage from "@/pages/admin-platform/OrganizationDetailPage";
import OrganizationsPage from "@/pages/admin-platform/OrganizationsPage";
import InvitationsTab from "@/pages/admin-settings/InvitationsTab";
import LiveTV from "@/pages/LiveTV";
import { AdminContextRedirect, AdminContextSelection } from "./BloemAdminShell";
import { AdminAccessGroups, LiveTVWatch } from "./lazyPages";

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
    <Route path="platform/compatibility" element={<CompatibilityApplicationsPage />} />
    <Route path="livetv" element={<AdminLiveTV />} />
  </>
);

/** Organization-context admin pages plus the context chooser and fallback redirect. */
export const bloemAdminContextRoutes = (
  <>
    <Route element={<OrganizationContextGuard />}>
      <Route path="organization" element={<OrganizationOverviewPage />} />
      <Route path="organization/people" element={<PeoplePage />} />
      <Route path="organization/policy-cohorts" element={<EntitlementCohortsPage />} />
      <Route path="organization/access-groups" element={<AdminAccessGroups />} />
      <Route path="organization/libraries" element={<LibrariesEntitlementsPage />} />
      <Route
        path="organization/invitations"
        element={
          <div className="page-shell py-4 sm:py-6">
            <InvitationsTab />
          </div>
        }
      />
      <Route path="organization/policy-decisions" element={<PolicyDecisionsPage />} />
      <Route path="organization/activity" element={<ActivityAuditPage />} />
      <Route path="organization/*" element={<Navigate to="/admin/organization" replace />} />
    </Route>
    <Route path="context" element={<AdminContextSelection />} />
    <Route path="*" element={<AdminContextRedirect />} />
  </>
);

/** Profile settings plus the direct-account profile credentials panel. */
export function withDirectProfileCredentials(settings: ReactElement) {
  return (
    <>
      {settings}
      <DirectProfileCredentials />
    </>
  );
}

/** Item detail wrapped with engagement campaign placements. */
export function withItemCampaigns(detail: ReactElement) {
  return <ItemCampaigns>{detail}</ItemCampaigns>;
}
