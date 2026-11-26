import { sourceCodeSettingsGroup } from "@/bloem/sourceCode/nav";
import {
  visibleUserSettingsGroups as siloGroups,
  type UserSettingsNavAccess,
  type UserSettingsNavGroup,
} from "@/lib/userSettingsNav";

export * from "@/lib/userSettingsNav";

/**
 * Silo's personal settings groups for useUserSettingsGroups.ts (and so the
 * settings list, sidebar and search), with Bloem's About group last: the
 * Source code page, for every profile (bloem-preview-plugin.ts).
 */
export function visibleUserSettingsGroups(access: UserSettingsNavAccess): UserSettingsNavGroup[] {
  return [...siloGroups(access), sourceCodeSettingsGroup()];
}
