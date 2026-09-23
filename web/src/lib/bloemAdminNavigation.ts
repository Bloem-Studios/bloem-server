// Bloem admin navigation, merged into the Silo-owned adminNavigation.ts.
import {
  Building2,
  CalendarClock,
  FileKey2,
  GitBranch,
  Library,
  Radio,
  ScrollText,
  Send,
  ShieldCheck,
  LayoutDashboard,
  Users,
  UsersRound,
} from "lucide-react";

import type { AdminNavGroup, AdminNavItem } from "@/lib/adminNavigation";

export const ORGANIZATION_ADMIN_NAV_SECTIONS: AdminNavGroup[] = [
  {
    label: "Organization",
    items: [
      {
        label: "Overview",
        description: "Organization health, membership and policy revisions.",
        keywords: ["organization", "tenant", "health"],
        icon: LayoutDashboard,
        href: "/admin/organization",
        exact: true,
      },
      {
        label: "People",
        description: "Organization memberships and profiles.",
        keywords: ["people", "memberships", "profiles"],
        icon: Users,
        href: "/admin/organization/people",
      },
      {
        label: "Policy Cohorts",
        description: "Immutable organization policy revisions and membership counts.",
        keywords: ["entitlements", "cohorts", "policy", "revisions"],
        icon: GitBranch,
        href: "/admin/organization/policy-cohorts",
      },
      {
        label: "Access Groups",
        description: "Organization access groups and profile assignments.",
        keywords: ["groups", "roles", "permissions"],
        icon: UsersRound,
        href: "/admin/organization/access-groups",
      },
      {
        label: "Libraries & Entitlements",
        description: "Organization libraries and platform-granted media ceilings.",
        keywords: ["libraries", "entitlements", "media ceiling"],
        icon: Library,
        href: "/admin/organization/libraries",
      },
      {
        label: "Invitations",
        description: "Invite administrators and members to this organization.",
        keywords: ["invitations", "invite", "members"],
        icon: Send,
        href: "/admin/organization/invitations",
      },
      {
        label: "Policy Decisions",
        description: "Inspect authorization decisions for this organization.",
        keywords: ["policy", "decisions", "authorization"],
        icon: ShieldCheck,
        href: "/admin/organization/policy-decisions",
      },
      {
        label: "Activity & Audit",
        description: "Organization and entitlement lifecycle history.",
        keywords: ["activity", "audit", "events"],
        icon: ScrollText,
        href: "/admin/organization/activity",
      },
    ],
  },
];

/** Platform administration pages, listed first in the Overview group. */
const BLOEM_PLATFORM_OVERVIEW_ITEMS: AdminNavItem[] = [
  {
    label: "Organizations",
    description: "Organization directory, lifecycle and memberships.",
    keywords: ["organizations", "tenants", "memberships"],
    icon: Building2,
    href: "/admin/platform/organizations",
  },
  {
    label: "Entitlement Templates",
    description: "Reusable tenant-member policies and revision history.",
    keywords: ["entitlements", "templates", "downloads", "profiles", "streams"],
    icon: FileKey2,
    href: "/admin/platform/entitlement-templates",
  },
  {
    label: "Campaigns & Seasonal Packs",
    description: "Author promotional cards, artwork and seasonal schedules.",
    keywords: ["promotions", "campaigns", "ambience", "seasonal", "artwork"],
    icon: CalendarClock,
    href: "/admin/platform/engagement",
  },
  {
    label: "Direct Accounts",
    description: "Apply entitlement templates to platform-managed accounts.",
    keywords: ["direct accounts", "entitlements", "products", "templates"],
    icon: Users,
    href: "/admin/platform/direct-accounts",
  },
  {
    label: "Bulk Account Policies",
    description: "Review authoritative direct-account policy and run exact bulk assignments.",
    keywords: ["direct accounts", "bulk", "cohorts", "policy", "entitlements"],
    icon: GitBranch,
    href: "/admin/platform/direct-accounts/bulk",
  },
];

const LIVE_TV_ITEM: AdminNavItem = {
  label: "Live TV",
  description: "OTA tuners, channel lineup, guide sources, DVR, and transcoding.",
  keywords: ["ota", "hdhomerun", "epg", "dvr", "guide", "schedules direct"],
  icon: Radio,
  href: "/admin/livetv",
};

/**
 * Adds Bloem's platform pages to Silo's admin navigation: platform items lead
 * the Overview group and Live TV follows Requests.
 */
export function withBloemAdminNav(sections: AdminNavGroup[]): AdminNavGroup[] {
  return sections.map((section) => {
    const items =
      section.label === "Overview"
        ? [...BLOEM_PLATFORM_OVERVIEW_ITEMS, ...section.items]
        : [...section.items];
    const requests = items.findIndex((item) => item.href === "/admin/requests");
    if (requests >= 0) items.splice(requests + 1, 0, LIVE_TV_ITEM);
    return { ...section, items };
  });
}

/** Navigation for an organization administrative context. */
export function buildOrganizationAdminNavSections(): AdminNavGroup[] {
  return ORGANIZATION_ADMIN_NAV_SECTIONS.map((section) => ({
    ...section,
    items: [...section.items],
  }));
}
