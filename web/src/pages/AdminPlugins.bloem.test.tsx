// @vitest-environment jsdom
// Bloem: the plugins page's storage tab, shown inside an administrative
// context (AdminPlugins.tsx).
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import AdminPlugins from "./AdminPlugins";

const mocks = vi.hoisted(() => ({
  active: { key: "platform", scope: "platform" } as object | null,
}));

vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => (mocks.active ? { active: mocks.active } : null),
}));
vi.mock("@/components/admin/plugins/StorageSourcesPanel", () => ({
  StorageSourcesPanel: () => <div>Storage sources panel</div>,
}));
vi.mock("@/components/admin/plugins/CommunityCatalogControl", () => ({
  CommunityCatalogControl: () => null,
}));
vi.mock("@/components/admin/plugins/PluginSourcePanels", () => ({
  PluginRepositoriesPanel: () => null,
  PluginUploadPanel: () => null,
}));
vi.mock("@/components/admin/plugins/PluginProvisioningStatus", () => ({
  PluginProvisioningStatus: () => null,
}));
vi.mock("@/hooks/queries/admin/tasks", () => ({ useTask: () => ({ data: undefined }) }));
vi.mock("@/hooks/queries/admin/plugins", () => ({
  CHECK_PLUGIN_UPDATES_TASK_KEY: "check-plugin-updates",
  useAdminPlugins: () => ({
    installations: [],
    catalog: [],
    catalogSettings: undefined,
    repositories: [],
    repositoriesError: null,
    isLoading: false,
  }),
  useAdminPluginProvisioningStatus: () => ({
    data: [],
    isError: false,
    isFetching: false,
    refetch: vi.fn(),
  }),
  useCheckPluginUpdates: () => ({ mutate: vi.fn(), isPending: false }),
}));

function SearchProbe() {
  return <output data-testid="search">{useLocation().search}</output>;
}

function renderPage(entry: string) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[entry]}>
        <AdminPlugins />
        <SearchProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("AdminPlugins storage tab", () => {
  afterEach(() => {
    cleanup();
    mocks.active = { key: "platform", scope: "platform" };
  });

  it("opens the storage tab from the URL inside an administrative context", () => {
    renderPage("/admin/plugins?tab=storage");
    expect(screen.getByRole("tab", { name: "Storage" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Storage sources panel")).toBeInTheDocument();
  });

  it("puts the storage tab in the URL and leaves it for the installed tab", () => {
    renderPage("/admin/plugins");
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Storage" }), { button: 0 });
    expect(screen.getByTestId("search")).toHaveTextContent("?tab=storage");
    expect(screen.getByText("Storage sources panel")).toBeInTheDocument();
    fireEvent.mouseDown(screen.getAllByRole("tab")[0]!, { button: 0 });
    expect(screen.getByTestId("search")).toBeEmptyDOMElement();
    expect(screen.queryByText("Storage sources panel")).not.toBeInTheDocument();
  });

  it("has no storage tab outside an administrative context", () => {
    mocks.active = null;
    renderPage("/admin/plugins?tab=storage");
    expect(screen.queryByRole("tab", { name: "Storage" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("tab")[0]).toHaveAttribute("aria-selected", "true");
  });
});
