// Bloem admin navigation, merged into the Silo-owned adminNavigation.ts.
import {
  Building2,
  CalendarClock,
  FileKey2,
  GitBranch,
  HardDrive,
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
        description: "Members, libraries and recent changes at a glance.",
        keywords: ["organization", "tenant", "health"],
        icon: LayoutDashboard,
        href: "/admin/organization",
        exact: true,
      },
      {
        label: "People",
        description: "Everyone in this organization, their profiles and their plan.",
        keywords: ["people", "memberships", "profiles"],
        icon: Users,
        href: "/admin/organization/people",
      },
      {
        label: "Cohorts",
        description: "Groups of accounts that share the same settings.",
        keywords: ["entitlements", "cohorts", "policy", "revisions"],
        icon: GitBranch,
        href: "/admin/organization/policy-cohorts",
      },
      {
        label: "Access Groups",
        description: "Groups that set what their profiles can do.",
        keywords: ["groups", "roles", "permissions"],
        icon: UsersRound,
        href: "/admin/organization/access-groups",
      },
      {
        label: "Libraries",
        description: "Libraries this organization owns or has been given.",
        keywords: ["libraries", "entitlements", "media ceiling", "grants"],
        icon: Library,
        href: "/admin/organization/libraries",
      },
      {
        label: "Storage sources",
        description: "S3 buckets this organization reads its ebooks from.",
        keywords: ["storage", "s3", "buckets", "sources", "ebooks"],
        icon: HardDrive,
        href: "/admin/organization/storage",
      },
      {
        label: "Invitations",
        description: "Invite administrators and members to this organization.",
        keywords: ["invitations", "invite", "members"],
        icon: Send,
        href: "/admin/organization/invitations",
      },
      {
        label: "Access Checks",
        description: "Why someone was allowed or refused something.",
        keywords: ["policy", "decisions", "authorization", "access", "denied"],
        icon: ShieldCheck,
        href: "/admin/organization/policy-decisions",
      },
      {
        label: "Activity",
        description: "Who changed what in this organization, and when.",
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
    description: "Every organization on this server, and its members.",
    keywords: ["organizations", "tenants", "memberships"],
    icon: Building2,
    href: "/admin/platform/organizations",
  },
  {
    label: "Plans",
    description: "What an account gets: libraries, screens, quality and downloads.",
    keywords: ["plans", "entitlements", "templates", "downloads", "profiles", "streams", "screens"],
    icon: FileKey2,
    href: "/admin/platform/entitlement-templates",
  },
  {
    label: "Campaigns & Seasonal Packs",
    description: "Promotions, artwork and seasonal looks for the home screen.",
    keywords: ["promotions", "campaigns", "ambience", "seasonal", "artwork"],
    icon: CalendarClock,
    href: "/admin/platform/engagement",
  },
  {
    label: "Direct Accounts",
    description: "Put an account that isn't in an organization on a plan.",
    keywords: ["direct accounts", "plans", "entitlements", "products", "templates"],
    icon: Users,
    href: "/admin/platform/direct-accounts",
  },
  {
    label: "Change Many Accounts",
    description: "Move many direct accounts to a plan or change their settings at once.",
    keywords: ["direct accounts", "bulk", "cohorts", "policy", "plans", "entitlements"],
    icon: GitBranch,
    href: "/admin/platform/direct-accounts/bulk",
  },
];

const LIVE_TV_ITEM: AdminNavItem = {
  label: "Live TV",
  description: "Tuners, channels, the TV guide, recordings and conversion.",
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

export function withBloemAdminSettings(
  items: AdminSettingsSearchItem[],
): AdminSettingsSearchItem[] {
  return [
    ...items,
    {
      id: "announcements",
      label: "Announcements",
      description: "Publish messages to viewers and withdraw earlier announcements.",
      settings: [{ label: "New announcement" }, { label: "Published announcements" }],
      keywords: ["engagement", "banner", "alert", "audience", "broadcast", "inbox"],
      icon: Megaphone,
    },
  ];
}

/** Adds the owned settings page through Silo's existing topic structure. */
export function withBloemSettingsNavigation(sections: AdminNavSection[]): AdminNavSection[] {
  const item: AdminNavItem = {
    label: "Announcements",
    description: "Publish messages to viewers.",
    icon: Megaphone,
    href: "/admin/settings/announcements",
  };
  return sections.map((section) => ({
    ...section,
    entries: section.entries.map((entry) =>
      "items" in entry && entry.id === "integrations"
        ? { ...entry, items: [...entry.items, item] }
        : entry,
    ),
  }));
}
