import {
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { AutoscanSource } from "@/api/types";
import { v2 } from "./request";

export async function readAdminAutoscanSources(
  profileContext: ProfileRequestContextSnapshot,
): Promise<AutoscanSource[]> {
  const rows: AutoscanSource[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let page = 0; page < 100; page++) {
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    const result = await v2("GET /api/v2/admin/autoscan/sources", {
      profileContext,
      query: { limit: 100, cursor },
    });
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    rows.push(
      ...result.items.map((row) => ({
        ...row,
        poll_interval_seconds: row.poll_interval_seconds ?? null,
        last_run_at: row.last_run_at ?? null,
        last_error: row.last_error ?? null,
      })),
    );
    if (!result.page || typeof result.page.has_more !== "boolean")
      throw new Error("Invalid autoscan source page.");
    if (!result.page.has_more) return rows;
    const next = result.page.next_cursor;
    if (!next || seen.has(next)) throw new Error("Invalid autoscan source continuation.");
    seen.add(next);
    cursor = next;
  }
  throw new Error("Too many autoscan sources to display.");
}
