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
  UserRoundKey,
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

/** Bloem's switch for household profile passwords, shown under Sign-in & Security. */
const PROFILE_PASSWORDS_SETTINGS: AdminSettingsSearchItem = {
  id: "profile-passwords",
  label: "Profile passwords",
  description: "Whether household profiles can sign in with their own email and password.",
  settings: [{ label: "Profile passwords" }],
  keywords: ["profile sign-in", "profile login", "household", "profile email", "credentials"],
  icon: UserRoundKey,
};

export function withBloemAdminSettings(
  items: AdminSettingsSearchItem[],
): AdminSettingsSearchItem[] {
  return [
    ...items,
    PROFILE_PASSWORDS_SETTINGS,
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

/** Adds the owned settings pages through Silo's existing topic structure. */
export function withBloemSettingsNavigation(sections: AdminNavSection[]): AdminNavSection[] {
  const item: AdminNavItem = {
    label: "Announcements",
    description: "Publish messages to viewers.",
    icon: Megaphone,
    href: "/admin/settings/announcements",
  };
  const profilePasswords: AdminNavItem = {
    label: PROFILE_PASSWORDS_SETTINGS.label,
    description: PROFILE_PASSWORDS_SETTINGS.description,
    keywords: [
      "settings",
      "configuration",
      PROFILE_PASSWORDS_SETTINGS.label,
      ...(PROFILE_PASSWORDS_SETTINGS.keywords ?? []),
    ],
    settings: PROFILE_PASSWORDS_SETTINGS.settings,
    icon: PROFILE_PASSWORDS_SETTINGS.icon,
    href: "/admin/settings/profile-passwords",
  };
  return sections.map((section) => ({
    ...section,
    entries: section.entries.map((entry) => {
      if (!("items" in entry)) return entry;
      if (entry.id === "integrations") return { ...entry, items: [...entry.items, item] };
      if (entry.id === "access") {
        // Right after Silo's Sign-in page, which it extends.
        const items = [...entry.items];
        const signIn = items.findIndex((page) => page.href === "/admin/settings/sign-in");
        items.splice(signIn + 1, 0, profilePasswords);
        return { ...entry, items };
      }
      return entry;
    }),
  }));
}
