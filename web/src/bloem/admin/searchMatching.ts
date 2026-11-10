import { bloemT } from "@/bloem/i18n";

/**
 * Bloem's extra Meilisearch matching strategy for the admin search settings,
 * listed after Silo's: `frequency` drops the most common query words first.
 */
export function bloemSearchMatchingOptions(): { value: string; label: string }[] {
  return [{ value: "frequency", label: bloemT("bloem.admin.search.matching.frequency") }];
}
