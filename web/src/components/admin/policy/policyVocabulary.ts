// Bloem: the words admin screens use for plans, cohorts and account policy.
// Keep every policy screen on these helpers so one setting reads the same
// everywhere. "Plan" is an entitlement template, a "screen" is a stream and a
// "conversion" is a transcode; see docs/architecture/bloem-plans.md.
import { AdminV2ClientError } from "@/api/adminV2Client";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { PERMISSION_MARKER_EDIT, PERMISSION_METADATA_CURATION } from "@/lib/permissions";

export interface NamedLibrary {
  id: number;
  name: string;
}

export interface PolicyValues {
  library_ids: number[] | null;
  playback_allowed: boolean;
  max_streams: number;
  max_profiles: number;
  transcode_allowed: boolean;
  audio_transcode_allowed?: boolean;
  max_transcodes: number;
  download_allowed: boolean;
  download_transcode_allowed: boolean;
  max_playback_quality: string;
  allowed_permissions: string[] | null;
  requests_allowed: boolean;
}

/** Permissions a plan or cohort can grant, with the label admins see. */
export const POLICY_PERMISSIONS: ReadonlyArray<readonly [string, string]> = [
  [PERMISSION_METADATA_CURATION, "Edit metadata"],
  [PERMISSION_MARKER_EDIT, "Edit intro and credits markers"],
  [PERMISSION_WATCH_LIVE_TV, "Watch Live TV"],
];

/** Picture-quality choices, in the order admins read them. */
export const PLAYBACK_QUALITIES: ReadonlyArray<readonly [string, string]> = [
  ["", "Any quality"],
  ["original", "Original quality"],
  ["1080p", "Up to 1080p"],
  ["2160p", "Up to 4K"],
];

export function permissionLabel(permission: string): string {
  return POLICY_PERMISSIONS.find(([value]) => value === permission)?.[1] ?? humanize(permission);
}

export function qualityLabel(quality: string): string {
  return PLAYBACK_QUALITIES.find(([value]) => value === quality)?.[1] ?? quality;
}

export function limitLabel(value: number, noun: string): string {
  return value === 0 ? "No limit" : `${value.toLocaleString()} ${value === 1 ? noun : `${noun}s`}`;
}

export function yesNo(value: boolean): string {
  return value ? "Yes" : "No";
}

export function libraryListLabel(ids: number[] | null, libraries: NamedLibrary[] = []): string {
  if (ids === null) return "All libraries";
  if (ids.length === 0) return "None";
  const names = new Map(libraries.map((library) => [library.id, library.name]));
  return ids.map((id) => names.get(id) ?? `Library ${id}`).join(", ");
}

export function permissionListLabel(permissions: string[] | null): string {
  if (permissions === null) return "All permissions";
  if (permissions.length === 0) return "None";
  return permissions.map(permissionLabel).join(", ");
}

/** The settings rows shown wherever a complete policy is displayed. */
export function policyRows(
  policy: PolicyValues,
  libraries: NamedLibrary[] = [],
): Array<[string, string]> {
  const rows: Array<[string, string]> = [
    ["Libraries", libraryListLabel(policy.library_ids, libraries)],
    ["Plays video", yesNo(policy.playback_allowed)],
    ["Screens at once", limitLabel(policy.max_streams, "screen")],
    ["Profiles", limitLabel(policy.max_profiles, "profile")],
    ["Video conversion", yesNo(policy.transcode_allowed)],
  ];
  if (policy.audio_transcode_allowed !== undefined)
    rows.push(["Audio conversion", yesNo(policy.audio_transcode_allowed)]);
  rows.push(
    ["Conversions at once", limitLabel(policy.max_transcodes, "conversion")],
    ["Download original files", yesNo(policy.download_allowed)],
    ["Download converted copies", yesNo(policy.download_transcode_allowed)],
    ["Best picture quality", qualityLabel(policy.max_playback_quality)],
    ["Media requests", yesNo(policy.requests_allowed)],
    ["Extra permissions", permissionListLabel(policy.allowed_permissions)],
  );
  return rows;
}

const FIELD_LABELS: Record<string, string> = {
  library_ids: "libraries",
  libraries: "libraries",
  playback_allowed: "whether video plays",
  max_streams: "screens at once",
  max_profiles: "profile limit",
  transcode_allowed: "video conversion",
  audio_transcode_allowed: "audio conversion",
  max_transcodes: "conversions at once",
  download_allowed: "original-file downloads",
  download_transcode_allowed: "converted-copy downloads",
  max_playback_quality: "best picture quality",
  allowed_permissions: "extra permissions",
  permissions: "extra permissions",
  requests_allowed: "media requests",
};

/** A policy field name from the server, as a phrase for a sentence. */
export function policyFieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? humanize(field).toLowerCase();
}

const STATE_LABELS: Record<string, string> = {
  managed: "Follows a cohort",
  custom: "Has its own settings",
  legacy_unmanaged: "Older settings, not in a cohort",
};

export function policyStateLabel(state: string): string {
  return STATE_LABELS[state] ?? humanize(state);
}

const CHANGE_KIND_LABELS: Record<string, string> = {
  assign_entitlement_cohort: "Move to a cohort",
  apply_entitlement_template: "Put on a plan",
  derive_entitlement_cohort: "Change some settings",
  restore_default_entitlement: "Reset to the organization default",
};

export function changeKindLabel(kind: string): string {
  return CHANGE_KIND_LABELS[kind] ?? humanize(kind);
}

const DERIVATION_LABELS: Record<string, string> = {
  exact_template: "From a plan",
  policy_patch: "Changed from another cohort",
  managed_default: "Organization default",
};

export function derivationLabel(kind: string): string {
  return DERIVATION_LABELS[kind] ?? humanize(kind);
}

const RESULT_REASON_LABELS: Record<string, string> = {
  already_applied: "already had these settings",
  no_profiles: "has no profiles",
  not_found: "no longer exists",
  protected_owner: "is the server owner and can't be changed here",
  mutation_failed: "couldn't be saved",
  authorization_state_changed: "your admin access changed while this ran",
  actor_authority_target: "skipped so you keep your own admin access",
  stale: "changed after the preview",
};

/** Why one account in a bulk change was skipped or failed. */
export function resultReasonLabel(reason: string): string {
  return RESULT_REASON_LABELS[reason] ?? humanize(reason).toLowerCase();
}

const JOB_HEADINGS: Record<string, string> = {
  queued: "Waiting to start",
  running: "Applying the change",
  completed: "Change applied",
  failed: "Change failed",
  cancelled: "Change stopped",
};

export function jobHeading(status: string): string {
  return JOB_HEADINGS[status] ?? humanize(status);
}

const ERROR_MESSAGES: Record<string, string> = {
  policy_confirmation_stale:
    "Something changed since the preview, or it expired. Review the accounts and preview again.",
  selection_expired: "The account list expired. Review the accounts again.",
  authorization_state_changed: "Your admin access changed. Review the accounts again.",
  not_found: "A plan, cohort or account could not be found.",
  idempotency_conflict:
    "This change was already submitted with different settings. Review it and apply again.",
  job_not_cancellable: "This change has already finished, so it can't be stopped.",
  entitlements_unavailable: "Plans are unavailable right now. Try again in a moment.",
  validation_failed: "Some settings aren't valid. Check the change and try again.",
};

/** A safe message for an admin policy API error. Never echoes server detail. */
export function policyErrorMessage(error: unknown): string {
  if (error instanceof AdminV2ClientError && ERROR_MESSAGES[error.code])
    return ERROR_MESSAGES[error.code]!;
  return "The change couldn't be completed. Try again.";
}

/** Errors after which the admin has to review the accounts again. */
export function needsFreshSelection(error: unknown): boolean {
  return (
    error instanceof AdminV2ClientError &&
    ["policy_confirmation_stale", "selection_expired", "authorization_state_changed"].includes(
      error.code,
    )
  );
}

const ACTION_LABELS: Record<string, string> = {
  playback_admission: "Start playback",
  direct_play: "Play",
  transcode: "Convert video",
  audio_transcode: "Convert audio",
  download: "Download",
  download_transcode: "Download a converted copy",
};

/** An authorization action, as admins say it. */
export function accessActionLabel(action: string): string {
  return ACTION_LABELS[action] ?? humanize(action);
}

const ACCESS_REASON_LABELS: Record<string, string> = {
  custom_denial: "A custom rule refused it",
  unknown_permission: "The permission isn't known",
  user_disabled: "The account is disabled",
  admin_role_required: "Only administrators can do this",
  primary_profile_required: "Only the household's main profile can do this",
  marker_edit_permission_required: "Needs permission to edit intro and credits markers",
  metadata_curation_permission_required: "Needs permission to edit metadata",
  item_outside_user_libraries: "The title is in a library they can't use",
  unknown_action: "The action isn't known",
  downloads_disabled: "Downloads are switched off on this server",
  download_permission_required: "Their settings don't allow downloads",
  transcode_disabled: "Video conversion is switched off",
  transcoding_disabled: "Video conversion is switched off",
  audio_transcoding_disabled: "Audio conversion is switched off",
  download_transcode_permission_required: "Their settings don't allow converted downloads",
  download_artifacts_unavailable: "No downloadable copy is ready yet",
  quality_ceiling_exceeded: "Above their best allowed picture quality",
  content_rating_exceeded: "Above the profile's age rating",
  max_streams_exceeded: "Every screen they're allowed was already in use",
  max_transcodes_exceeded: "Every conversion they're allowed was already in use",
};

/** Why an authorization check was refused (or empty for an allowed one). */
export function accessReasonLabel(code: string): string {
  if (!code) return "No reason recorded";
  return ACCESS_REASON_LABELS[code] ?? humanize(code);
}

/** One policy value from a preview, as admins read it. */
const LIMIT_FIELDS = new Set(["max_streams", "max_profiles", "max_transcodes"]);

export function formatPolicyValue(field: string, value: unknown): string {
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (field === "max_playback_quality" && typeof value === "string") return qualityLabel(value);
  if (LIMIT_FIELDS.has(field) && value === 0) return "No limit";
  if (field === "allowed_permissions" || field === "permissions")
    return Array.isArray(value) || value === null
      ? permissionListLabel(value as string[] | null)
      : String(value);
  if (value === null || value === undefined) return field.includes("librar") ? "All" : "None";
  if (Array.isArray(value)) return value.length ? value.join(", ") : "None";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

export function formatDateTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function formatTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { timeStyle: "short" }).format(date);
}

function humanize(value: string): string {
  const text = value.replace(/[_.]+/g, " ").trim();
  return text ? text[0]!.toUpperCase() + text.slice(1) : value;
}
