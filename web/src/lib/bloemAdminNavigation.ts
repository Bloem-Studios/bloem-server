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
  Megaphone,
} from "lucide-react";

import type { AdminNavGroup, AdminNavItem, AdminNavSection } from "@/lib/adminNavigation";
import type { AdminSettingsSearchItem } from "@/lib/adminSettingsSearch";

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
export function withBloemAdminNav(sections: AdminNavSection[]): AdminNavSection[] {
  return [
    { label: "Platform", entries: [...BLOEM_PLATFORM_OVERVIEW_ITEMS] },
    ...sections.map((section) => {
      const entries = [...section.entries];
      const requests = entries.findIndex((entry) => "items" in entry && entry.id === "requests");
      if (requests >= 0) entries.splice(requests + 1, 0, LIVE_TV_ITEM);
      return { ...section, entries };
    }),
  ];
}

/** Navigation for an organization administrative context. */
export function buildOrganizationAdminNavSections(): AdminNavSection[] {
  return ORGANIZATION_ADMIN_NAV_SECTIONS.map((section) => ({
    label: section.label,
    entries: [...section.items],
  }));
}

export function withBloemAdminSettings(items: AdminSettingsSearchItem[]): AdminSettingsSearchItem[] {
  return [...items, {
    id: "announcements",
    label: "Announcements",
    description: "Publish messages to viewers and withdraw earlier announcements.",
    settings: [{ label: "New announcement" }, { label: "Published announcements" }],
    keywords: ["engagement", "banner", "alert", "audience", "broadcast", "inbox"],
    icon: Megaphone,
  }];
}

/** Adds the owned settings page through Silo's existing topic structure. */
export function withBloemSettingsNavigation(sections: AdminNavSection[]): AdminNavSection[] {
 const item:AdminNavItem={label:"Announcements",description:"Publish messages to viewers.",icon:Megaphone,href:"/admin/settings/announcements"};
 return sections.map(section=>({...section,entries:section.entries.map(entry=>"items" in entry && entry.id==="integrations"?{...entry,items:[...entry.items,item]}:entry)}));
}
