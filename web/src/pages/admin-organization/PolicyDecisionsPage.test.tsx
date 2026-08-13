// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import PolicyDecisionsPage from "./PolicyDecisionsPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: { key: "organization:org-1", scope: "organization", name: "North Sea Media" },
  }),
}));

describe("PolicyDecisionsPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("shows a redacted structured explanation without policy mutation controls", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const decision = {
        id: 41,
        timestamp: "2026-08-13T08:00:00Z",
        organization: { id: "org-1", membership_id: "membership-1" },
        subject: { account_id: 17, profile_id: "profile-7" },
        group: { id: 4, name: "Kids" },
        library_ceiling: [2, 7],
        action: "download",
        resource: { folder_id: 7, title: "Visible", access_token: "[redacted]" },
        allowed: false,
        reason_code: "downloads_disabled",
        policy_versions: [
          { kind: "vendor", version: 13 },
          { kind: "custom", name: "downloads", version: 5 },
        ],
      };
      if (path === "/organization/policy-decisions?limit=50") {
        return { decisions: [decision] } as never;
      }
      if (path === "/organization/policy-decisions/41") return { decision } as never;
      throw new Error(`unexpected ${path}`);
    });

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <PolicyDecisionsPage />
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByRole("button", { name: /download.*denied/i }));
    expect(await screen.findByText("downloads_disabled")).toBeInTheDocument();
    expect(screen.getByText("[redacted]")).toBeInTheDocument();
    expect(screen.getByText(/Account 17/)).toBeInTheDocument();
    expect(screen.getByText(/Profile profile-7/)).toBeInTheDocument();
    expect(screen.getByText(/Kids/)).toBeInTheDocument();
    expect(screen.getByText(/2, 7/)).toBeInTheDocument();
    expect(screen.getByText(/vendor.*13/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /activate|save policy|new document/i })).toBeNull();
  });

  it("loads the next cursor page without dropping earlier decisions", async () => {
    const decision = (id: number, action: string) => ({
      id,
      timestamp: "2026-08-13T08:00:00Z",
      organization: { id: "org-1" },
      subject: {},
      group: {},
      library_ceiling: [],
      action,
      resource: {},
      allowed: true,
      reason_code: "allowed",
      policy_versions: [],
    });
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/policy-decisions?limit=50") {
        return { decisions: [decision(1, "play")], next_cursor: "next page" } as never;
      }
      if (path === "/organization/policy-decisions?limit=50&cursor=next%20page") {
        return { decisions: [decision(2, "download")] } as never;
      }
      throw new Error(`unexpected ${path}`);
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <PolicyDecisionsPage />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole("button", { name: /play.*allowed/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Load more decisions" }));
    expect(await screen.findByRole("button", { name: /download.*allowed/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /play.*allowed/i })).toBeInTheDocument();
  });
});
