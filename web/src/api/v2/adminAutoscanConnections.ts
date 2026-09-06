import {
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { AutoscanConnection } from "@/api/types";
import { v2 } from "./request";

export async function readAdminAutoscanConnections(
  profileContext: ProfileRequestContextSnapshot,
): Promise<AutoscanConnection[]> {
  const rows: AutoscanConnection[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let page = 0; page < 100; page++) {
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    const result = await v2("GET /api/v2/admin/autoscan/connections", {
      profileContext,
      query: { limit: 100, cursor },
    });
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    rows.push(...result.items);
    if (!result.page || typeof result.page.has_more !== "boolean")
      throw new Error("Invalid autoscan connection page.");
    if (!result.page.has_more) return rows;
    const next = result.page.next_cursor;
    if (!next || seen.has(next)) throw new Error("Invalid autoscan connection continuation.");
    seen.add(next);
    cursor = next;
  }
  throw new Error("Too many autoscan connections to display.");
}
