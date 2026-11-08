// Bloem: the draft behind the bulk policy change form, and how it becomes an
// API command. Kept apart from the form so the rules are easy to test.
import type {
  EntitlementCohort,
  PolicyCommand,
  PolicyPatch,
} from "@/hooks/queries/admin/entitlementCohorts";
import {
  libraryListLabel,
  permissionListLabel,
  qualityLabel,
  type NamedLibrary,
} from "./policyVocabulary";

export type PolicyOperation = "" | "assign" | "template" | "derive" | "restore";
export type TriState = "unchanged" | "true" | "false";
type SetMode = "unchanged" | "add" | "remove" | "replace";

export const KEEP = "unchanged";

export interface PatchDraft {
  librariesMode: SetMode | "all" | "none";
  libraryIDs: number[];
  permissionsMode: SetMode | "unrestricted";
  permissions: string[];
  playback: TriState;
  transcode: TriState;
  downloads: TriState;
  transcodedDownloads: TriState;
  requests: TriState;
  maxStreams: string;
  maxProfiles: string;
  maxTranscodes: string;
  /** "unchanged", or a value from PLAYBACK_QUALITIES. */
  maxPlaybackQuality: string;
}

export interface PolicyChangeDraft {
  operation: PolicyOperation;
  cohortID: string;
  planKey: string;
  planRevision: string;
  derivedName: string;
  patch: PatchDraft;
}

export interface PlanChoice {
  key: string;
  name: string;
  /** The newest revision. Revisions run from 1 to this number. */
  revision: number;
}

export const emptyPatchDraft: PatchDraft = {
  librariesMode: KEEP,
  libraryIDs: [],
  permissionsMode: KEEP,
  permissions: [],
  playback: KEEP,
  transcode: KEEP,
  downloads: KEEP,
  transcodedDownloads: KEEP,
  requests: KEEP,
  maxStreams: "",
  maxProfiles: "",
  maxTranscodes: "",
  maxPlaybackQuality: KEEP,
};

export function emptyPolicyChangeDraft(
  operation: PolicyOperation = "",
  cohortID = "",
): PolicyChangeDraft {
  return {
    operation,
    cohortID,
    planKey: "",
    planRevision: "",
    derivedName: "",
    patch: emptyPatchDraft,
  };
}

/**
 * Builds the API command for a draft, or returns the message to show next to
 * the form when something is missing.
 */
export function buildPolicyCommand(
  draft: PolicyChangeDraft,
  cohorts: EntitlementCohort[],
  includeCustomProfiles: boolean,
): PolicyCommand | Error {
  const activeCohort = cohorts.some(
    (cohort) => cohort.cohort_id === draft.cohortID && !cohort.archived,
  );
  switch (draft.operation) {
    case "assign":
      if (!activeCohort) return new Error("Choose a cohort.");
      return {
        kind: "assign_entitlement_cohort",
        cohort_id: draft.cohortID,
        include_custom_profiles: includeCustomProfiles,
      };
    case "template": {
      const revision = Number(draft.planRevision);
      if (!draft.planKey.trim()) return new Error("Choose a plan.");
      if (!Number.isInteger(revision) || revision <= 0)
        return new Error("Choose which revision of the plan to use.");
      return {
        kind: "apply_entitlement_template",
        template_key: draft.planKey.trim(),
        template_revision: revision,
        include_custom_profiles: includeCustomProfiles,
      };
    }
    case "restore":
      return {
        kind: "restore_default_entitlement",
        include_custom_profiles: includeCustomProfiles,
      };
    case "derive": {
      if (!activeCohort) return new Error("Choose the cohort to start from.");
      if (!draft.derivedName.trim()) return new Error("Name the new cohort.");
      const patch = buildPatch(draft.patch);
      if (patch instanceof Error) return patch;
      if (Object.keys(patch).length === 0) return new Error("Change at least one setting.");
      return {
        kind: "derive_entitlement_cohort",
        cohort_id: draft.cohortID,
        name: draft.derivedName.trim(),
        patch,
        include_custom_profiles: includeCustomProfiles,
      };
    }
    default:
      return new Error("Choose what should change.");
  }
}

export function buildPatch(input: PatchDraft): PolicyPatch | Error {
  const draft = normalizePatchDraft(input);
  const patch: PolicyPatch = {};
  if (draft.librariesMode === "all" || draft.librariesMode === "none") {
    patch.libraries = { mode: draft.librariesMode };
  } else if (draft.librariesMode !== KEEP) {
    if (!draft.libraryIDs.length) return new Error("Pick at least one library.");
    patch.libraries = {
      mode: draft.librariesMode,
      values: [...new Set(draft.libraryIDs)].sort((a, b) => a - b),
    };
  }
  if (draft.permissionsMode === "unrestricted") {
    patch.permissions = { mode: "unrestricted" };
  } else if (draft.permissionsMode !== KEEP) {
    if (!draft.permissions.length) return new Error("Pick at least one permission.");
    patch.permissions = {
      mode: draft.permissionsMode,
      values: [...new Set(draft.permissions)].sort(),
    };
  }
  const booleans: Array<[TriState, keyof PolicyPatch]> = [
    [draft.playback, "playback_allowed"],
    [draft.transcode, "transcode_allowed"],
    [draft.downloads, "download_allowed"],
    [draft.transcodedDownloads, "download_transcode_allowed"],
    [draft.requests, "requests_allowed"],
  ];
  for (const [value, key] of booleans)
    if (value !== KEEP) (patch[key] as boolean | undefined) = value === "true";
  const numbers: Array<[string, "max_streams" | "max_profiles" | "max_transcodes", string]> = [
    [draft.maxStreams, "max_streams", "Screens at once"],
    [draft.maxProfiles, "max_profiles", "Profiles"],
    [draft.maxTranscodes, "max_transcodes", "Conversions at once"],
  ];
  for (const [value, key, label] of numbers) {
    if (!value.trim()) continue;
    const parsed = Number(value);
    if (!Number.isInteger(parsed) || parsed < 0)
      return new Error(`${label} must be a whole number, 0 or more.`);
    patch[key] = parsed;
  }
  if (draft.maxPlaybackQuality !== KEEP) patch.max_playback_quality = draft.maxPlaybackQuality;
  return patch;
}

/** Applies the rules that follow from switching playback, conversion or downloads off. */
export function normalizePatchDraft(draft: PatchDraft): PatchDraft {
  const next = { ...draft };
  if (next.playback === "false")
    return {
      ...next,
      maxStreams: "0",
      transcode: "false",
      maxTranscodes: "0",
      downloads: "false",
      transcodedDownloads: "false",
      maxPlaybackQuality: "",
    };
  if (next.transcode === "false") next.maxTranscodes = "0";
  if (next.downloads === "false") next.transcodedDownloads = "false";
  return next;
}

/** One line per changed setting, in the admin's words. */
export function describePatchDraft(draft: PatchDraft, libraries: NamedLibrary[] = []): string[] {
  const lines: string[] = [];
  const setVerb: Record<string, string> = { add: "Add", remove: "Remove", replace: "Only" };
  if (draft.librariesMode === "all") lines.push("Libraries: all libraries");
  else if (draft.librariesMode === "none") lines.push("Libraries: none");
  else if (draft.librariesMode !== KEEP)
    lines.push(
      `Libraries: ${setVerb[draft.librariesMode]} ${draft.libraryIDs.length ? libraryListLabel(draft.libraryIDs, libraries) : "(pick some)"}`,
    );
  if (draft.permissionsMode === "unrestricted") lines.push("Extra permissions: all");
  else if (draft.permissionsMode !== KEEP)
    lines.push(
      `Extra permissions: ${setVerb[draft.permissionsMode]} ${draft.permissions.length ? permissionListLabel(draft.permissions) : "(pick some)"}`,
    );
  const booleans: Array<[string, TriState]> = [
    ["Plays video", draft.playback],
    ["Video conversion", draft.transcode],
    ["Download original files", draft.downloads],
    ["Download converted copies", draft.transcodedDownloads],
    ["Media requests", draft.requests],
  ];
  for (const [label, value] of booleans)
    if (value !== KEEP) lines.push(`${label}: ${value === "true" ? "allowed" : "not allowed"}`);
  const numbers: Array<[string, string]> = [
    ["Screens at once", draft.maxStreams],
    ["Profiles", draft.maxProfiles],
    ["Conversions at once", draft.maxTranscodes],
  ];
  for (const [label, value] of numbers)
    if (value.trim()) lines.push(`${label}: ${value.trim() === "0" ? "no limit" : value.trim()}`);
  if (draft.maxPlaybackQuality !== KEEP)
    lines.push(`Best picture quality: ${qualityLabel(draft.maxPlaybackQuality).toLowerCase()}`);
  return lines;
}
