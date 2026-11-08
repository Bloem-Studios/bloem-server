import type { StorageSource } from "@/hooks/queries/admin/storageSources";

export type SourceStatus = "live" | "setup" | "off" | "removed";

/**
 * Whether a source is serving files, in the four states an administrator
 * cares about: live (a library can read from it), needs setup (installed but
 * never connected), off (installed but not running) and removed (its plugin
 * is gone; it can't come back).
 */
export function sourceStatus(source: StorageSource): SourceStatus {
  if (source.installation_id === null || source.state === "detached") return "removed";
  if (!source.enabled || source.state !== "attached") return "off";
  if (!source.configured) return "setup";
  return "live";
}
