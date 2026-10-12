// Contract fixtures from the mixed-locations Task 11 client brief, stored in
// the real /api/v2 wire shape (string file IDs) and mapped through the same
// adapter the web app uses for GET /api/v2/catalog/items/{id}.
import type { ItemDetail } from "@/api/types";
import { catalogItemDetailFromV2 } from "@/api/v2/catalog";
import type { components } from "@/api/v2/schema";
import f1 from "./f1-local-native-multipart.json";
import f2 from "./f2-single-track-truncation.json";
import f4 from "./f4-movie-multipart.json";
import f7 from "./f7-capabilities.json";

type CatalogItemDetailV2 = components["schemas"]["CatalogItemDetail"];

export const F1_WIRE: CatalogItemDetailV2 = f1;
export const F2_WIRE: CatalogItemDetailV2 = f2;
export const F4_WIRE: CatalogItemDetailV2 = f4;
export const F7_CAPABILITIES = f7;

export const F1_LOCAL_ID = "group:7301928374650001";
export const F1_NATIVE_ID =
  "group:bloem-storage:audiobook:c3bc04036a1ccc16936c2b53adc7a7ba608b6527d48a8a28231301fddd6d521a";

/** A fresh, deeply frozen detail so any mutation of the cached payload throws. */
export function fixtureItem(wire: CatalogItemDetailV2): ItemDetail {
  return deepFreeze(catalogItemDetailFromV2(structuredClone(wire)));
}

function deepFreeze<T>(value: T): T {
  if (value && typeof value === "object") {
    for (const child of Object.values(value)) deepFreeze(child);
    Object.freeze(value);
  }
  return value;
}
