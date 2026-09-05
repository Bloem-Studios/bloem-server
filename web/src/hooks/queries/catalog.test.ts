import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import getCatalogFiltersOk from "../../../../contracts/api/v2/fixtures/get_catalog_filters_ok.json";
import searchCatalogFacetOk from "../../../../contracts/api/v2/fixtures/search_catalog_facet_ok.json";

import { setProfileId } from "@/api/client";
import { createEmptyQueryDefinition } from "@/api/types";
import { installPolicyStorageMocks, jsonResponse } from "@/pages/admin-policy/policyTestUtils";

import { fetchCatalogFacetSearch, fetchCatalogFilters, fetchCatalogPage } from "./catalog";

type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function stubFetch(body: unknown): FetchMock {
  const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(body));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function requestedUrl(fetchMock: FetchMock): URL {
  return new URL(String(fetchMock.mock.calls[0]?.[0]), "http://localhost");
}

describe("catalog browse and facets on the v2 contract", () => {
  beforeEach(() => {
    installPolicyStorageMocks();
    setProfileId("p-owner");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches query-source facets and flattens the technical block", async () => {
    const fetchMock = stubFetch(getCatalogFiltersOk);

    const filters = await fetchCatalogFilters({
      source: "query",
      query_definition: createEmptyQueryDefinition(),
    });

    const url = requestedUrl(fetchMock);
    expect(url.pathname).toBe("/api/v2/catalog/filters");
    expect(url.searchParams.get("source")).toBe("query");
    expect(url.searchParams.has("skip_technical")).toBe(false);
    expect(filters.genres).toEqual(["Crime"]);
    expect(filters.authors).toEqual(["Frank Herbert"]);
    expect(filters.resolutions).toEqual(["2160p"]);
    expect(filters.subtitle_languages).toEqual(["en"]);
    expect(filters).not.toHaveProperty("technical");
  });

  it("asks for lightweight facets with skip_technical and never forwards the overlay", async () => {
    const fetchMock = stubFetch({ ...getCatalogFiltersOk, technical: undefined });

    await fetchCatalogFilters(
      {
        source: "query",
        q: "house",
        library_id: 7,
        query_definition: createEmptyQueryDefinition(),
      },
      undefined,
      { includeTechnical: false },
    );

    const url = requestedUrl(fetchMock);
    expect(url.searchParams.get("skip_technical")).toBe("true");
    expect(url.searchParams.get("library_id")).toBe("7");
    expect(url.searchParams.has("q")).toBe(false);
    expect(url.searchParams.has("sort")).toBe(false);
  });

  it("searches one facet by prefix", async () => {
    const fetchMock = stubFetch(searchCatalogFacetOk);

    const result = await fetchCatalogFacetSearch(
      { source: "query", query_definition: createEmptyQueryDefinition() },
      "author",
      "fra",
      10,
    );

    const url = requestedUrl(fetchMock);
    expect(url.pathname).toBe("/api/v2/catalog/filters/search");
    expect(url.searchParams.get("facet")).toBe("author");
    expect(url.searchParams.get("q")).toBe("fra");
    expect(url.searchParams.get("limit")).toBe("10");
    expect(result).toEqual({ matches: ["Frank Herbert"], has_more: true });
  });

  it("keeps the browse on the v1 offset endpoint until v2 carries rule groups", async () => {
    const fetchMock = stubFetch({ items: [], total: 0, has_more: false });

    await fetchCatalogPage(
      { source: "query", library_id: 7, query_definition: createEmptyQueryDefinition() },
      60,
      120,
      undefined,
      false,
    );

    expect(String(fetchMock.mock.calls[0]?.[0])).toBe(
      "/api/v1/catalog?source=query&library_id=7&sort=added_at&order=desc&limit=60&offset=120&include_total=false",
    );
  });
});
