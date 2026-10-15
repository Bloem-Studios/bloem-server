// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";

import StorageSourcesPage from "./StorageSourcesPage";

const mockUseAdminContext = vi.hoisted(() => vi.fn());
const sourcesQuery = vi.hoisted(() => ({ current: {} as Record<string, unknown> }));

vi.mock("@/contexts/AdminContextProvider", () => ({ useAdminContext: mockUseAdminContext }));
vi.mock("@/hooks/queries/admin/storageSources", () => ({
  useStorageSources: () => sourcesQuery.current,
}));
vi.mock("@/components/admin/plugins/StorageSourcesPanel", () => ({
  StorageSourcesPanel: () => <section aria-label="Storage sources panel">panel</section>,
}));

const organization = { key: "organization:org-1", scope: "organization", name: "Acme Books" };

describe("StorageSourcesPage", () => {
  beforeEach(() => {
    mockUseAdminContext.mockReturnValue({ active: organization });
    sourcesQuery.current = { data: [], isSuccess: true, error: null };
  });

  it("renders only the storage sources panel under the organization's name", () => {
    render(<StorageSourcesPage />);
    expect(screen.getByRole("heading", { name: "Storage sources" })).toBeInTheDocument();
    expect(screen.getByText(/Acme Books/)).toBeInTheDocument();
    expect(screen.getByLabelText("Storage sources panel")).toBeInTheDocument();
    // Nothing of the platform Plugins page.
    expect(screen.queryByText(/installed/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/catalog/i)).not.toBeInTheDocument();
  });

  it.each([403, 404])("explains an HTTP %i from the sources list instead of failing", (status) => {
    sourcesQuery.current = {
      error: new AdminV2ClientError(status, "insufficient_organization_authority", "no"),
      isError: true,
    };
    render(<StorageSourcesPage />);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Storage sources aren't enabled for this organization. Ask your platform administrator.",
    );
    expect(screen.queryByLabelText("Storage sources panel")).not.toBeInTheDocument();
  });

  it("leaves other load failures to the panel", () => {
    sourcesQuery.current = {
      error: new AdminV2ClientError(500, "internal", "boom"),
      isError: true,
    };
    render(<StorageSourcesPage />);
    expect(screen.getByLabelText("Storage sources panel")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("asks for an organization when the platform context is active", () => {
    mockUseAdminContext.mockReturnValue({ active: { key: "platform", scope: "platform" } });
    render(<StorageSourcesPage />);
    expect(screen.getByRole("alert")).toHaveTextContent(/Select an organization/);
    expect(screen.queryByLabelText("Storage sources panel")).not.toBeInTheDocument();
  });
});
