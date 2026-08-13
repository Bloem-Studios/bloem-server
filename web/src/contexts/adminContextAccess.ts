import type { AdminContextSummary } from "@/api/types";

/** The shell is visible only while authority is resolving or after discovery found a usable context. */
export function canRenderAdminShell(
  active: AdminContextSummary | null,
  available: readonly AdminContextSummary[],
  switching: boolean,
): boolean {
  return switching || active !== null || available.length > 0;
}
