import type { EntitlementTemplatePolicy } from "@/api/types";
import { PERMISSION_MARKER_EDIT, PERMISSION_METADATA_CURATION } from "@/lib/permissions";

function permissionLabel(permission: string) {
  if (permission === PERMISSION_METADATA_CURATION) return "Metadata curation";
  if (permission === PERMISSION_MARKER_EDIT) return "Marker editing";
  return permission;
}

export function EntitlementPolicyDetails({ policy }: { policy: EntitlementTemplatePolicy }) {
  const values = [
    policy.library_ids === null
      ? "Libraries: all enabled"
      : policy.library_ids.length > 0
        ? `Libraries: ${policy.library_ids.join(", ")}`
        : "Libraries: none",
    `Playback ${policy.playback_allowed ? "allowed" : "blocked"}`,
    `${policy.max_streams} max streams`,
    `${policy.max_profiles} max profiles`,
    `Playback transcoding ${policy.transcode_allowed ? "allowed" : "blocked"}`,
    `${policy.max_transcodes} max transcodes`,
    `Original downloads ${policy.download_allowed ? "allowed" : "blocked"}`,
    `Transcoded downloads ${policy.download_transcode_allowed ? "allowed" : "blocked"}`,
    `Maximum quality: ${policy.max_playback_quality || "unrestricted"}`,
    `Media requests ${policy.requests_allowed ? "allowed" : "blocked"}`,
    policy.allowed_permissions === null
      ? "Permissions unrestricted"
      : policy.allowed_permissions.length > 0
        ? `Permissions: ${policy.allowed_permissions.map(permissionLabel).join(", ")}`
        : "Permissions: none",
  ];

  return (
    <ul className="text-muted-foreground grid gap-1 text-xs sm:grid-cols-2">
      {values.map((value) => (
        <li key={value}>{value}</li>
      ))}
    </ul>
  );
}
