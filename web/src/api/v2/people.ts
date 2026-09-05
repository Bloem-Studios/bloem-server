/**
 * Viewer-facing people reads and the coalescing refresh action on the v2
 * contract. The page still models a person with a numeric id, so the
 * adapter converts the wire id at the boundary.
 */
import type { Person } from "@/api/types";
import { personFromV2 } from "@/api/v2/catalog";
import { v2, type V2Result } from "@/api/v2/request";

export type PersonRefreshResult = V2Result<"POST /api/v2/catalog/people/{id}/refresh">;

export async function searchPeople(query: string, limit = 20): Promise<Person[]> {
  const people = await v2("GET /api/v2/catalog/people", { query: { q: query, limit } });
  return people.items.map(personFromV2);
}

export async function getPerson(id: string): Promise<Person> {
  return personFromV2(await v2("GET /api/v2/catalog/people/{id}", { path: { id } }));
}

/** Queues a metadata refresh for a person; the server coalesces repeats. */
export async function refreshPerson(id: string): Promise<PersonRefreshResult> {
  return v2("POST /api/v2/catalog/people/{id}/refresh", { path: { id } });
}
