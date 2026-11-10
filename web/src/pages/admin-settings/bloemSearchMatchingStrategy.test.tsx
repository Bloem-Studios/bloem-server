import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import SearchSettings from "./SearchSettings";
import { fakeSettingsForm } from "./settingsForm.test-support";

const useSettingsFormMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => new Set<string>(),
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useCheckAdminSettingsConnection: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCatalogSearchStatus: () => ({ data: undefined, isLoading: true }),
}));

function renderWithStrategy(strategy: string) {
  // The strategy sits under the page's Advanced disclosure.
  localStorage.setItem("silo.admin.advanced.search", "true");
  useSettingsFormMock.mockReturnValue(
    fakeSettingsForm(
      {
        "catalog.search.provider": "meilisearch",
        "catalog.search.meilisearch.matching_strategy": strategy,
      },
      { buildConnectionCheckRequest: vi.fn(() => ({ values: {}, dirty_keys: [] })) },
    ),
  );
  render(
    <MemoryRouter>
      <SearchSettings />
    </MemoryRouter>,
  );
}

describe("SearchSettings matching strategy", () => {
  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it("names the trailing-words strategy", () => {
    renderWithStrategy("last");
    expect(screen.getByText("Drop trailing words until something matches")).toBeTruthy();
  });

  it("offers dropping the most common words first", () => {
    renderWithStrategy("frequency");
    expect(screen.getByText("Drop the most common words until something matches")).toBeTruthy();
  });
});
