// Bloem: the words admin screens use for plans, cohorts and account policy.
// Keep every policy screen on these helpers so one setting reads the same
// everywhere. "Plan" is an entitlement template, a "screen" is a stream and a
// "conversion" is a transcode; see docs/architecture/bloem-plans.md.
import { AdminV2ClientError } from "@/api/adminV2Client";
import { bloemT, type BloemMessageKey } from "@/bloem/i18n";
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

/** A `[value, label]` pair whose label is a getter, so it follows the interface language. */
function choice(value: string, label: BloemMessageKey): readonly [string, string] {
  const entry: [string, string] = [value, ""];
  Object.defineProperty(entry, 1, { enumerable: true, get: () => bloemT(label) });
  return entry;
}

/** Permissions a plan or cohort can grant, with the label admins see. */
export const POLICY_PERMISSIONS: ReadonlyArray<readonly [string, string]> = [
  choice(PERMISSION_METADATA_CURATION, "bloem.admin.policy.vocabulary.permission.editMetadata"),
  choice(PERMISSION_MARKER_EDIT, "bloem.admin.policy.vocabulary.permission.editMarkers"),
  choice(PERMISSION_WATCH_LIVE_TV, "bloem.admin.policy.vocabulary.permission.watchLiveTv"),
];

/** Picture-quality choices, in the order admins read them. */
export const PLAYBACK_QUALITIES: ReadonlyArray<readonly [string, string]> = [
  choice("", "bloem.admin.policy.vocabulary.quality.any"),
  choice("original", "bloem.admin.policy.vocabulary.quality.original"),
  choice("1080p", "bloem.admin.policy.vocabulary.quality.upTo1080p"),
  choice("2160p", "bloem.admin.policy.vocabulary.quality.upTo4k"),
];

export function permissionLabel(permission: string): string {
  return POLICY_PERMISSIONS.find(([value]) => value === permission)?.[1] ?? humanize(permission);
}

export function qualityLabel(quality: string): string {
  return PLAYBACK_QUALITIES.find(([value]) => value === quality)?.[1] ?? quality;
}

/** What a limit counts: a screen is a stream, a conversion a transcode. */
export type LimitNoun = "screen" | "profile" | "conversion";

const LIMIT_COUNTS: Record<LimitNoun, BloemMessageKey> = {
  screen: "bloem.admin.policy.vocabulary.limit.screens",
  profile: "bloem.admin.policy.vocabulary.limit.profiles",
  conversion: "bloem.admin.policy.vocabulary.limit.conversions",
};

export function limitLabel(value: number, noun: LimitNoun): string {
  return value === 0
    ? bloemT("bloem.admin.policy.vocabulary.noLimit")
    : bloemT(LIMIT_COUNTS[noun], { count: value });
}

export function yesNo(value: boolean): string {
  return value
    ? bloemT("bloem.admin.policy.vocabulary.yes")
    : bloemT("bloem.admin.policy.vocabulary.no");
}

export function libraryListLabel(ids: number[] | null, libraries: NamedLibrary[] = []): string {
  if (ids === null) return bloemT("bloem.admin.policy.vocabulary.allLibraries");
  if (ids.length === 0) return bloemT("bloem.admin.policy.vocabulary.none");
  const names = new Map(libraries.map((library) => [library.id, library.name]));
  return ids
    .map((id) => names.get(id) ?? bloemT("bloem.admin.policy.vocabulary.libraryNumber", { id }))
    .join(", ");
}

export function permissionListLabel(permissions: string[] | null): string {
  if (permissions === null) return bloemT("bloem.admin.policy.vocabulary.allPermissions");
  if (permissions.length === 0) return bloemT("bloem.admin.policy.vocabulary.none");
  return permissions.map(permissionLabel).join(", ");
}

/** The settings rows shown wherever a complete policy is displayed. */
export function policyRows(
  policy: PolicyValues,
  libraries: NamedLibrary[] = [],
): Array<[string, string]> {
  const row = (name: BloemMessageKey, value: string): [string, string] => [bloemT(name), value];
  const rows: Array<[string, string]> = [
    row(
      "bloem.admin.policy.vocabulary.setting.libraries",
      libraryListLabel(policy.library_ids, libraries),
    ),
    row("bloem.admin.policy.vocabulary.setting.playsVideo", yesNo(policy.playback_allowed)),
    row(
      "bloem.admin.policy.vocabulary.setting.screensAtOnce",
      limitLabel(policy.max_streams, "screen"),
    ),
    row(
      "bloem.admin.policy.vocabulary.setting.profiles",
      limitLabel(policy.max_profiles, "profile"),
    ),
    row("bloem.admin.policy.vocabulary.setting.videoConversion", yesNo(policy.transcode_allowed)),
  ];
  if (policy.audio_transcode_allowed !== undefined)
    rows.push(
      row(
        "bloem.admin.policy.vocabulary.setting.audioConversion",
        yesNo(policy.audio_transcode_allowed),
      ),
    );
  rows.push(
    row(
      "bloem.admin.policy.vocabulary.setting.conversionsAtOnce",
      limitLabel(policy.max_transcodes, "conversion"),
    ),
    row("bloem.admin.policy.vocabulary.setting.downloadOriginals", yesNo(policy.download_allowed)),
    row(
      "bloem.admin.policy.vocabulary.setting.downloadConverted",
      yesNo(policy.download_transcode_allowed),
    ),
    row(
      "bloem.admin.policy.vocabulary.setting.bestQuality",
      qualityLabel(policy.max_playback_quality),
    ),
    row("bloem.admin.policy.vocabulary.setting.mediaRequests", yesNo(policy.requests_allowed)),
    row(
      "bloem.admin.policy.vocabulary.setting.extraPermissions",
      permissionListLabel(policy.allowed_permissions),
    ),
  );
  return rows;
}

const FIELD_LABELS: Record<string, BloemMessageKey> = {
  library_ids: "bloem.admin.policy.vocabulary.field.libraries",
  libraries: "bloem.admin.policy.vocabulary.field.libraries",
  playback_allowed: "bloem.admin.policy.vocabulary.field.playback",
  max_streams: "bloem.admin.policy.vocabulary.field.screensAtOnce",
  max_profiles: "bloem.admin.policy.vocabulary.field.profileLimit",
  transcode_allowed: "bloem.admin.policy.vocabulary.field.videoConversion",
  audio_transcode_allowed: "bloem.admin.policy.vocabulary.field.audioConversion",
  max_transcodes: "bloem.admin.policy.vocabulary.field.conversionsAtOnce",
  download_allowed: "bloem.admin.policy.vocabulary.field.originalDownloads",
  download_transcode_allowed: "bloem.admin.policy.vocabulary.field.convertedDownloads",
  max_playback_quality: "bloem.admin.policy.vocabulary.field.bestQuality",
  allowed_permissions: "bloem.admin.policy.vocabulary.field.extraPermissions",
  permissions: "bloem.admin.policy.vocabulary.field.extraPermissions",
  requests_allowed: "bloem.admin.policy.vocabulary.field.mediaRequests",
};

/** A policy field name from the server, as a phrase for a sentence. */
export function policyFieldLabel(field: string): string {
  const id = FIELD_LABELS[field];
  return id ? bloemT(id) : humanize(field).toLowerCase();
}

const STATE_LABELS: Record<string, BloemMessageKey> = {
  managed: "bloem.admin.policy.vocabulary.state.managed",
  custom: "bloem.admin.policy.vocabulary.state.custom",
  legacy_unmanaged: "bloem.admin.policy.vocabulary.state.legacyUnmanaged",
};

export function policyStateLabel(state: string): string {
  const id = STATE_LABELS[state];
  return id ? bloemT(id) : humanize(state);
}

const CHANGE_KIND_LABELS: Record<string, BloemMessageKey> = {
  assign_entitlement_cohort: "bloem.admin.policy.vocabulary.changeKind.assignCohort",
  apply_entitlement_template: "bloem.admin.policy.vocabulary.changeKind.applyPlan",
  derive_entitlement_cohort: "bloem.admin.policy.vocabulary.changeKind.deriveCohort",
  restore_default_entitlement: "bloem.admin.policy.vocabulary.changeKind.restoreDefault",
};

export function changeKindLabel(kind: string): string {
  const id = CHANGE_KIND_LABELS[kind];
  return id ? bloemT(id) : humanize(kind);
}

const DERIVATION_LABELS: Record<string, BloemMessageKey> = {
  exact_template: "bloem.admin.policy.vocabulary.derivation.exactTemplate",
  policy_patch: "bloem.admin.policy.vocabulary.derivation.policyPatch",
  managed_default: "bloem.admin.policy.vocabulary.derivation.managedDefault",
};

export function derivationLabel(kind: string): string {
  const id = DERIVATION_LABELS[kind];
  return id ? bloemT(id) : humanize(kind);
}

const RESULT_REASON_LABELS: Record<string, BloemMessageKey> = {
  already_applied: "bloem.admin.policy.vocabulary.resultReason.alreadyApplied",
  no_profiles: "bloem.admin.policy.vocabulary.resultReason.noProfiles",
  not_found: "bloem.admin.policy.vocabulary.resultReason.notFound",
  protected_owner: "bloem.admin.policy.vocabulary.resultReason.protectedOwner",
  mutation_failed: "bloem.admin.policy.vocabulary.resultReason.mutationFailed",
  authorization_state_changed: "bloem.admin.policy.vocabulary.resultReason.authorizationChanged",
  actor_authority_target: "bloem.admin.policy.vocabulary.resultReason.actorAuthorityTarget",
  stale: "bloem.admin.policy.vocabulary.resultReason.stale",
};

/** Why one account in a bulk change was skipped or failed. */
export function resultReasonLabel(reason: string): string {
  const id = RESULT_REASON_LABELS[reason];
  return id ? bloemT(id) : humanize(reason).toLowerCase();
}

const JOB_HEADINGS: Record<string, BloemMessageKey> = {
  queued: "bloem.admin.policy.vocabulary.job.queued",
  running: "bloem.admin.policy.vocabulary.job.running",
  completed: "bloem.admin.policy.vocabulary.job.completed",
  failed: "bloem.admin.policy.vocabulary.job.failed",
  cancelled: "bloem.admin.policy.vocabulary.job.cancelled",
};

/** A heading for a bulk change that says what actually happened. */
export function jobHeading(job: {
  status: string;
  succeeded: number;
  skipped: unknown[];
  failed: unknown[];
}): string {
  const notChanged = job.skipped.length + job.failed.length;
  if (job.status === "completed" && notChanged > 0)
    return job.succeeded === 0
      ? bloemT("bloem.admin.policy.vocabulary.job.nothingChanged")
      : bloemT("bloem.admin.policy.vocabulary.job.someChanged");
  const id = JOB_HEADINGS[job.status];
  return id ? bloemT(id) : humanize(job.status);
}

const ERROR_MESSAGES: Record<string, BloemMessageKey> = {
  policy_confirmation_stale: "bloem.admin.policy.vocabulary.error.confirmationStale",
  selection_expired: "bloem.admin.policy.vocabulary.error.selectionExpired",
  authorization_state_changed: "bloem.admin.policy.vocabulary.error.authorizationChanged",
  not_found: "bloem.admin.policy.vocabulary.error.notFound",
  idempotency_conflict: "bloem.admin.policy.vocabulary.error.idempotencyConflict",
  job_not_cancellable: "bloem.admin.policy.vocabulary.error.jobNotCancellable",
  entitlements_unavailable: "bloem.admin.policy.vocabulary.error.entitlementsUnavailable",
  validation_failed: "bloem.admin.policy.vocabulary.error.validationFailed",
};

/** A safe message for an admin policy API error. Never echoes server detail. */
export function policyErrorMessage(error: unknown): string {
  const id = error instanceof AdminV2ClientError ? ERROR_MESSAGES[error.code] : undefined;
  return bloemT(id ?? "bloem.admin.policy.vocabulary.error.other");
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

const ACTION_LABELS: Record<string, BloemMessageKey> = {
  "silo.permission.decision": "bloem.admin.policy.vocabulary.action.permissionCheck",
  "data.silo.permission.decision": "bloem.admin.policy.vocabulary.action.permissionCheck",
  "silo.scope.decision": "bloem.admin.policy.vocabulary.action.libraryAccessCheck",
  "data.silo.scope.decision": "bloem.admin.policy.vocabulary.action.libraryAccessCheck",
  "silo.action.decision": "bloem.admin.policy.vocabulary.action.playbackOrDownloadCheck",
  "data.silo.action.decision": "bloem.admin.policy.vocabulary.action.playbackOrDownloadCheck",
  "data.silo.slow.decision": "bloem.admin.policy.vocabulary.action.check",
  playback_admission: "bloem.admin.policy.vocabulary.action.startPlayback",
  direct_play: "bloem.admin.policy.vocabulary.action.play",
  transcode: "bloem.admin.policy.vocabulary.action.convertVideo",
  audio_transcode: "bloem.admin.policy.vocabulary.action.convertAudio",
  download: "bloem.admin.policy.vocabulary.action.download",
  download_transcode: "bloem.admin.policy.vocabulary.action.downloadConverted",
};

/** An authorization action, as admins say it. */
export function accessActionLabel(action: string): string {
  const id = ACTION_LABELS[action];
  return id ? bloemT(id) : humanize(action);
}

const ACCESS_REASON_LABELS: Record<string, BloemMessageKey> = {
  custom_denial: "bloem.admin.policy.vocabulary.accessReason.customDenial",
  unknown_permission: "bloem.admin.policy.vocabulary.accessReason.unknownPermission",
  user_disabled: "bloem.admin.policy.vocabulary.accessReason.userDisabled",
  admin_role_required: "bloem.admin.policy.vocabulary.accessReason.adminRoleRequired",
  primary_profile_required: "bloem.admin.policy.vocabulary.accessReason.primaryProfileRequired",
  marker_edit_permission_required: "bloem.admin.policy.vocabulary.accessReason.markerEditRequired",
  metadata_curation_permission_required:
    "bloem.admin.policy.vocabulary.accessReason.metadataCurationRequired",
  item_outside_user_libraries: "bloem.admin.policy.vocabulary.accessReason.itemOutsideLibraries",
  unknown_action: "bloem.admin.policy.vocabulary.accessReason.unknownAction",
  downloads_disabled: "bloem.admin.policy.vocabulary.accessReason.downloadsDisabled",
  download_permission_required: "bloem.admin.policy.vocabulary.accessReason.downloadNotAllowed",
  transcode_disabled: "bloem.admin.policy.vocabulary.accessReason.videoConversionOff",
  transcoding_disabled: "bloem.admin.policy.vocabulary.accessReason.videoConversionOff",
  audio_transcoding_disabled: "bloem.admin.policy.vocabulary.accessReason.audioConversionOff",
  download_transcode_permission_required:
    "bloem.admin.policy.vocabulary.accessReason.convertedDownloadNotAllowed",
  download_artifacts_unavailable: "bloem.admin.policy.vocabulary.accessReason.noDownloadableCopy",
  quality_ceiling_exceeded: "bloem.admin.policy.vocabulary.accessReason.qualityCeiling",
  content_rating_exceeded: "bloem.admin.policy.vocabulary.accessReason.contentRating",
  max_streams_exceeded: "bloem.admin.policy.vocabulary.accessReason.maxStreams",
  max_transcodes_exceeded: "bloem.admin.policy.vocabulary.accessReason.maxTranscodes",
};

/** Why an authorization check was refused (or empty for an allowed one). */
export function accessReasonLabel(code: string): string {
  if (!code) return bloemT("bloem.admin.policy.vocabulary.accessReason.none");
  const id = ACCESS_REASON_LABELS[code];
  return id ? bloemT(id) : humanize(code);
}

/** One policy value from a preview, as admins read it. */
const LIMIT_FIELDS = new Set(["max_streams", "max_profiles", "max_transcodes"]);

export function formatPolicyValue(field: string, value: unknown): string {
  if (typeof value === "boolean") return yesNo(value);
  if (field === "max_playback_quality" && typeof value === "string") return qualityLabel(value);
  if (LIMIT_FIELDS.has(field) && value === 0)
    return bloemT("bloem.admin.policy.vocabulary.noLimit");
  if (field === "allowed_permissions" || field === "permissions")
    return Array.isArray(value) || value === null
      ? permissionListLabel(value as string[] | null)
      : String(value);
  if (value === null || value === undefined)
    return field.includes("librar")
      ? bloemT("bloem.admin.policy.vocabulary.all")
      : bloemT("bloem.admin.policy.vocabulary.none");
  if (Array.isArray(value))
    return value.length ? value.join(", ") : bloemT("bloem.admin.policy.vocabulary.none");
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
