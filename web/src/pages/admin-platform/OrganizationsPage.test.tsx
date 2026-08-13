// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import OrganizationsPage from "./OrganizationsPage";

vi.mock("@/api/adminV2Client", () => ({ adminV2Api: vi.fn() }));

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/admin/platform/organizations"]}>
        <Routes>
          <Route path="/admin/platform/organizations" element={<OrganizationsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("OrganizationsPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  it("debounces server search and advances with the returned cursor", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const url = String(path);
      if (url.includes("cursor=next-page")) {
        return {
          organizations: [
            {
              id: "2",
              name: "Zuider",
              slug: "zuider",
              status: "active",
              owner_account_id: 9,
              policy_revision: 1,
              membership_count: 1,
              profile_count: 0,
              library_count: 0,
              entitlement_count: 0,
            },
          ],
        } as never;
      }
      return {
        organizations: [
          {
            id: "1",
            name: "North Sea",
            slug: "north-sea",
            status: "active",
            owner_account_id: 7,
            policy_revision: 3,
            membership_count: 5,
            profile_count: 8,
            library_count: 2,
            entitlement_count: 1,
          },
        ],
        next_cursor: "next-page",
      } as never;
    });

    renderPage();
    expect(await screen.findByRole("link", { name: /North Sea/ })).toBeInTheDocument();

    fireEvent.change(screen.getByRole("searchbox", { name: "Search organizations" }), {
      target: { value: "north" },
    });
    await waitFor(() =>
      expect(
        vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).includes("query=north")),
      ).toBe(true),
    );

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByRole("link", { name: /Zuider/ })).toBeInTheDocument();
    expect(
      vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).includes("cursor=next-page")),
    ).toBe(true);
  });

  it("validates organization creation before sending it", async () => {
    vi.mocked(adminV2Api).mockResolvedValue({ organizations: [] } as never);
    renderPage();
    await screen.findByText("No organizations match these filters.");

    fireEvent.click(screen.getByRole("button", { name: "Create organization" }));
    fireEvent.change(screen.getByLabelText("Organization name"), {
      target: { value: "North Sea" },
    });
    fireEvent.change(screen.getByLabelText("Slug"), { target: { value: "Not Valid" } });
    fireEvent.click(screen.getByRole("button", { name: /^Create$/ }));

    expect(await screen.findByText(/lowercase letters, numbers, and hyphens/)).toBeInTheDocument();
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledTimes(1);
  });
});
