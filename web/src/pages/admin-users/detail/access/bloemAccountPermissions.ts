import { PERMISSION_WATCH_LIVE_TV } from "@/lib/permissions";

// Bloem permissions share the upstream card's draft, conflict and group rules.
export const bloemAccountPermissions = [
  {
    permission: PERMISSION_WATCH_LIVE_TV,
    label: "Watch Live TV",
    description:
      "Watch channels and manage recordings. Independent of library access; group restrictions still apply.",
  },
] as const;

export function bloemPermissionRestriction(permission: string, groupName: string | undefined) {
  return permission === PERMISSION_WATCH_LIVE_TV
    ? `${groupName} group doesn't allow Live TV`
    : undefined;
}
