import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { StorageArtifact, StorageSource } from "@/hooks/queries/admin/storageSources";

import { StorageSourcesPanel } from "./StorageSourcesPanel";

// Radix Select scrolls the selected option into view; jsdom has no layout.
window.HTMLElement.prototype.scrollIntoView ??= () => {};

const upgrade = vi.fn();
const install = vi.fn();

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
};
const artifacts: StorageArtifact[] = [
  {
    artifact_key: "bookwarehouse-0.2.0",
    plugin_id: "bloem.storage.bookwarehouse",
    version: "0.2.0",
    os: "linux",
    arch: "amd64",
  },
  {
    artifact_key: "other-1.0.0",
    plugin_id: "bloem.storage.other",
    version: "1.0.0",
    os: "linux",
    arch: "amd64",
  },
];

vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageSources: () => ({ data: [source], isSuccess: true, isError: false }),
  useStorageArtifacts: () => ({ data: artifacts, isSuccess: true, isError: false }),
  useUpgradeStorageSource: () => ({ mutate: upgrade, isPending: false }),
  useInstallStorageSource: () => ({ mutate: install, isPending: false }),
}));

describe("StorageSourcesPanel", () => {
  beforeEach(() => {
    upgrade.mockReset();
    install.mockReset();
  });

  it("lists sources with their state", () => {
    render(<StorageSourcesPanel />);
    const row = screen.getByText("books").closest("li")!;
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

  it("rejects configuration that is not an object of objects", () => {
    render(<StorageSourcesPanel />);
    fireEvent.click(screen.getByRole("button", { name: /add source/i }));
    fireEvent.change(screen.getByLabelText("Provider configuration"), {
      target: { value: '{"source": "plain"}' },
    });
    expect(screen.getByText("Configuration must be a JSON object of objects.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install" })).toBeDisabled();
  });
});
