// Bloem admin navigation, added to the Silo-owned adminNavigation.ts and
// adminSettingsSearch.ts after Silo builds its lists. Labels are getters, as
// Silo's are, so they follow the interface language.
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
  type LucideIcon,
} from "lucide-react";

import { bloemEnglishText, bloemT, type BloemMessageKey } from "@/bloem/i18n";
import type {
  AdminNavEntry,
  AdminNavGroup,
  AdminNavItem,
  AdminNavSection,
} from "@/lib/adminNavigation";
import type { AdminSettingsSearchItem } from "@/lib/adminSettingsSearch";

/** A Bloem page with a label and a description in Bloem's catalog. */
type NavName = BloemMessageKey extends infer K
  ? K extends `bloem.adminNav.${infer Name}.label`
    ? Name
    : never
  : never;

function englishTerms(...ids: BloemMessageKey[]): string[] {
  return ids.map((id) => bloemEnglishText(id));
}

function navItem(
  name: NavName,
  page: { keywords: readonly string[]; icon: LucideIcon; href: string; exact?: boolean },
): AdminNavItem {
  const label = `bloem.adminNav.${name}.label` as const;
  const description = `bloem.adminNav.${name}.description` as const;
  return {
    get label() {
      return bloemT(label);
    },
    get description() {
      return bloemT(description);
    },
    get keywords() {
      return [...page.keywords, ...englishTerms(label, description)];
    },
    icon: page.icon,
    href: page.href,
    exact: page.exact,
  };
}

/** A group or section heading's label and search terms, kept live (never spread: that freezes them). */
function withHeading<T extends object>(
  id: BloemMessageKey,
  rest: T,
): T & {
  readonly label: string;
  readonly keywords: readonly string[];
} {
  return Object.defineProperties(rest, {
    label: { enumerable: true, get: () => bloemT(id) },
    keywords: { enumerable: true, get: () => englishTerms(id) },
  }) as T & { readonly label: string; readonly keywords: readonly string[] };
}

export const ORGANIZATION_ADMIN_NAV_SECTIONS: AdminNavGroup[] = [
  withHeading("bloem.adminNav.section.organization", {
    items: [
      navItem("organizationOverview", {
        keywords: ["organization", "tenant", "health"],
        icon: LayoutDashboard,
        href: "/admin/organization",
        exact: true,
      }),
      navItem("organizationPeople", {
        keywords: ["people", "memberships", "profiles"],
        icon: Users,
        href: "/admin/organization/people",
      }),
      navItem("organizationCohorts", {
        keywords: ["entitlements", "cohorts", "policy", "revisions"],
        icon: GitBranch,
        href: "/admin/organization/policy-cohorts",
      }),
      navItem("organizationAccessGroups", {
        keywords: ["groups", "roles", "permissions"],
        icon: UsersRound,
        href: "/admin/organization/access-groups",
      }),
      navItem("organizationLibraries", {
        keywords: ["libraries", "entitlements", "media ceiling", "grants"],
        icon: Library,
        href: "/admin/organization/libraries",
      }),
      navItem("organizationStorage", {
        keywords: ["storage", "s3", "buckets", "sources", "ebooks"],
        icon: HardDrive,
        href: "/admin/organization/storage",
      }),
      navItem("organizationInvitations", {
        keywords: ["invitations", "invite", "members"],
        icon: Send,
        href: "/admin/organization/invitations",
      }),
      navItem("organizationAccessChecks", {
        keywords: ["policy", "decisions", "authorization", "access", "denied"],
        icon: ShieldCheck,
        href: "/admin/organization/policy-decisions",
      }),
      navItem("organizationActivity", {
        keywords: ["activity", "audit", "events"],
        icon: ScrollText,
        href: "/admin/organization/activity",
      }),
    ],
  }),
];

/** Platform administration pages, the sidebar's first section. */
const BLOEM_PLATFORM_OVERVIEW_ITEMS: AdminNavItem[] = [
  navItem("platformOrganizations", {
    keywords: ["organizations", "tenants", "memberships"],
    icon: Building2,
    href: "/admin/platform/organizations",
  }),
  navItem("platformPlans", {
    keywords: ["plans", "entitlements", "templates", "downloads", "profiles", "streams", "screens"],
    icon: FileKey2,
    href: "/admin/platform/entitlement-templates",
  }),
  navItem("platformEngagement", {
    keywords: ["promotions", "campaigns", "ambience", "seasonal", "artwork"],
    icon: CalendarClock,
    href: "/admin/platform/engagement",
  }),
  navItem("platformDirectAccounts", {
    keywords: ["direct accounts", "plans", "entitlements", "products", "templates"],
    icon: Users,
    href: "/admin/platform/direct-accounts",
  }),
  navItem("platformBulkAccounts", {
    keywords: ["direct accounts", "bulk", "cohorts", "policy", "plans", "entitlements"],
    icon: GitBranch,
    href: "/admin/platform/direct-accounts/bulk",
  }),
];

const LIVE_TV_ITEM = navItem("liveTv", {
  keywords: ["ota", "hdhomerun", "epg", "dvr", "guide", "schedules direct"],
  icon: Radio,
  href: "/admin/livetv",
});

/** `section` with other entries, keeping Silo's translated label live. */
function withEntries(section: AdminNavSection, entries: AdminNavEntry[]): AdminNavSection {
  return {
    id: section.id,
    get label() {
      return section.label;
    },
    get keywords() {
      return section.keywords;
    },
    entries,
  };
}

/**
 * Adds Bloem's platform pages to Silo's admin navigation: platform items lead
 * the sidebar and Live TV follows Requests.
 */
export function withBloemAdminNav(sections: AdminNavSection[]): AdminNavSection[] {
  return [
    withHeading("bloem.adminNav.section.platform", {
      id: "platform",
      entries: [...BLOEM_PLATFORM_OVERVIEW_ITEMS],
    }),
    ...sections.map((section) => {
      const entries = [...section.entries];
      const requests = entries.findIndex((entry) => "items" in entry && entry.id === "requests");
      if (requests >= 0) entries.splice(requests + 1, 0, LIVE_TV_ITEM);
      return withEntries(section, entries);
    }),
  ];
}

/** Navigation for an organization administrative context. */
export function buildOrganizationAdminNavSections(): AdminNavSection[] {
  return ORGANIZATION_ADMIN_NAV_SECTIONS.map((group) => ({
    id: "organization",
    get label() {
      return group.label;
    },
    get keywords() {
      return group.keywords;
    },
    entries: [...group.items],
  }));
}

function searchEntry(id: BloemMessageKey) {
  return {
    get label() {
      return bloemT(id);
    },
    get keywords() {
      return englishTerms(id);
    },
  };
}

/** Bloem's settings pages, appended to Silo's ADMIN_SETTINGS_NAV. */
export const BLOEM_ADMIN_SETTINGS: AdminSettingsSearchItem[] = [
  {
    id: "announcements",
    get label() {
      return bloemT("bloem.adminNav.announcementsSettings.label");
    },
    get description() {
      return bloemT("bloem.adminNav.announcementsSettings.description");
    },
    settings: [
      searchEntry("bloem.adminNav.announcementsSettings.newAnnouncement"),
      searchEntry("bloem.adminNav.announcementsSettings.published"),
    ],
    get keywords() {
      return [
        "engagement",
        "banner",
        "alert",
        "audience",
        "broadcast",
        "inbox",
        ...englishTerms(
          "bloem.adminNav.announcementsSettings.label",
          "bloem.adminNav.announcementsSettings.description",
        ),
      ];
    },
    icon: Megaphone,
  },
];

/**
 * Adds Bloem's settings page to Silo's Integrations topic. It changes Silo's
 * list in place: a copy would freeze Silo's translated labels.
 */
export function addBloemSettingsNavigation(sections: AdminNavSection[]): void {
  const item = navItem("announcements", {
    keywords: [],
    icon: Megaphone,
    href: "/admin/settings/announcements",
  });
  for (const section of sections) {
    for (const entry of section.entries) {
      if (!("items" in entry)) continue;
      if (entry.id === "integrations") entry.items.push(item);
    }
  }
}
