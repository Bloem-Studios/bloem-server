import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { StorageSourcesPanel } from "./StorageSourcesPanel";

// Radix Select scrolls the selected option into view; jsdom has no layout.
window.HTMLElement.prototype.scrollIntoView ??= () => {};

const upgrade = vi.fn();
const install = vi.fn();
const configure = vi.fn();

const source: StorageSource = {
  source_key: "413183ec-ece5-4805-aac4-6c31f6ddf0df",
  owner_kind: "platform",
  installation_id: 14,
  plugin_id: "bloem.storage.bookwarehouse",
  provider_source_id: "books",
  root_entry_id: "root",
  configuration_revision: 3,
  enabled: true,
  state: "attached",
  configured: true,
  connection_summary: {},
};
const artifacts: StorageArtifact[] = [
  {
    artifact_key: "bookwarehouse-0.2.0",
    plugin_id: "bloem.storage.bookwarehouse",
    version: "0.2.0",
    os: "linux",
    arch: "amd64",
    catalog: true,
    config_schema: [],
  },
  {
    artifact_key: "other-1.0.0",
    plugin_id: "bloem.storage.other",
    version: "1.0.0",
    os: "linux",
    arch: "amd64",
    catalog: true,
    config_schema: [],
  },
];

let artifactsQuery: Record<string, unknown> = {};
const refetch = vi.fn();

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageSources: () => ({ data: [source], isSuccess: true, isError: false }),
  useStorageArtifacts: () => ({
    data: artifacts,
    isSuccess: true,
    isError: false,
    isFetching: false,
    refetch,
    ...artifactsQuery,
  }),
  useUpgradeStorageSource: () => ({ mutate: upgrade, isPending: false }),
  useInstallStorageSource: () => ({ mutate: install, isPending: false }),
  useConfigureStorageSource: () => ({ mutate: configure, isPending: false }),
  useDiscoverStorageSource: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useListS3Buckets: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useListS3Folders: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useReplaceStorageConfiguration: () => ({ mutate: vi.fn(), isPending: false }),
  useRotateStorageCredentials: () => ({ mutate: vi.fn(), isPending: false }),
  useDisableStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useRemoveStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useTestStorageSource: () => ({ mutate: vi.fn(), isPending: false }),
  useStorageScope: () => ({ active: {}, scope: "platform", key: "platform" }),
}));

describe("StorageSourcesPanel", () => {
  beforeEach(() => {
    upgrade.mockReset();
    install.mockReset();
    configure.mockReset();
    refetch.mockReset();
    artifactsQuery = {};
    source.configured = true;
  });

  it("lists sources with their state", () => {
    render(<StorageSourcesPanel />);
    const row = screen.getByText("Bookwarehouse").closest("li")!;
    expect(within(row).getByText("attached")).toBeInTheDocument();
    expect(within(row).getByText(/bloem\.storage\.bookwarehouse/)).toBeInTheDocument();
  });

  it("offers only releases of the source's own plugin for an upgrade", () => {
    render(<StorageSourcesPanel />);
    fireEvent.click(screen.getByRole("button", { name: /upgrade/i }));
    fireEvent.click(screen.getByRole("combobox", { name: "Release to upgrade to" }));
    expect(screen.getByRole("option", { name: /bookwarehouse 0\.2\.0/ })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /bloem\.storage\.other/ })).not.toBeInTheDocument();
  });

  it("installs Bookwarehouse from the catalog without approval, uploads or JSON", () => {
    render(<StorageSourcesPanel />);
    fireEvent.click(screen.getByRole("button", { name: /add source/i }));
    expect(screen.queryByLabelText("Plugin executable")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Provider configuration")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Install Bookwarehouse" }));
    expect(install).toHaveBeenCalledWith(
      expect.objectContaining({
        artifactKey: "bookwarehouse-0.2.0",
        rootEntryId: "root",
        config: {},
      }),
      expect.any(Object),
    );
  });
  it("shows the Add dialog's Retry as loading while the catalog refetches", () => {
    artifactsQuery = {
      data: undefined,
      isSuccess: false,
      isError: true,
      error: new Error("boom"),
      isFetching: false,
    };
    const { rerender } = render(<StorageSourcesPanel />);
    fireEvent.click(screen.getByRole("button", { name: /add source/i }));
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalledTimes(1);

    artifactsQuery = { ...artifactsQuery, isFetching: true };
    rerender(<StorageSourcesPanel />);
    expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
  });
  it("asks for only URL and key after installation", () => {
    source.configured = false;
    render(<StorageSourcesPanel />);
    fireEvent.change(screen.getByLabelText("Bookwarehouse server URL"), {
      target: { value: "https://books.example" },
    });
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "synthetic-fixture-key" },
    });
    expect(screen.getByLabelText("API key")).toHaveAttribute("type", "password");
    fireEvent.click(screen.getByRole("button", { name: "Save connection" }));
    expect(configure).toHaveBeenCalledWith(
      { source, url: "https://books.example", apiKey: "synthetic-fixture-key" },
      expect.any(Object),
    );
  });
});
