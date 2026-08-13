// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import PeoplePage from "./PeoplePage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({
    active: {
      key: "organization:org-1",
      scope: "organization",
      organizationId: "org-1",
      name: "North Sea Media",
      status: "active",
      authority: "organization_admin",
      policyRevision: 7,
      securityRevision: 3,
    },
  }),
}));

const person = (accountId: number, name: string) => ({
  organization_id: "org-1",
  account_id: accountId,
  email: `${name.toLowerCase()}@example.test`,
  display_name: name,
  membership_id: `membership-${accountId}`,
  membership_status: "active",
  legacy_role: "user",
  security_revision: 4,
  last_activity: "2026-08-12T10:00:00Z",
  profiles: [
    {
      id: `profile-${accountId}`,
      name,
      group_id: 2,
      group_name: "Adults",
      updated_at: "2026-08-12T10:00:00Z",
    },
  ],
});

function LocationProbe() {
  const navigate = useNavigate();
  return (
    <>
      <output aria-label="current location">{useLocation().search}</output>
      <button type="button" onClick={() => navigate(-1)}>
        Browser back
      </button>
    </>
  );
}

function renderPage(initial = "/admin/organization/people") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[initial]}>
        <Routes>
          <Route
            path="/admin/organization/people"
            element={
              <>
                <PeoplePage />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...rendered, queryClient };
}

describe("PeoplePage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("keeps filters in the URL and replaces rather than merges cursor pages", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const url = String(path);
      if (url === "/organization/groups") return { groups: [] } as never;
      if (url.includes("cursor=next-page")) {
        return { items: [person(2, "Bea")], approximate_total: 2 } as never;
      }
      return {
        items: [person(1, "Ada")],
        next_cursor: "next-page",
        approximate_total: 2,
      } as never;
    });

    renderPage();
    expect((await screen.findAllByText("Ada")).length).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    expect((await screen.findAllByText("Bea")).length).toBeGreaterThan(0);
    expect(screen.queryAllByText("Ada")).toHaveLength(0);

    fireEvent.change(screen.getByRole("searchbox", { name: "Search people" }), {
      target: { value: "ada lovelace" },
    });
    fireEvent.submit(screen.getByRole("search"));

    await waitFor(() =>
      expect(screen.getByLabelText("current location")).toHaveTextContent("?query=ada+lovelace"),
    );
    expect(
      vi
        .mocked(adminV2Api)
        .mock.calls.some(([path]) => String(path).includes("query=ada+lovelace")),
    ).toBe(true);
  });

  it("creates a server selection from canonical filters and clears it when filters change", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") return { groups: [] } as never;
      if (path === "/organization/people/selections" && init?.method === "POST") {
        return {
          selection: {
            token: "opaque-signed-selection",
            matched: 1250,
            excluded: 3,
            expires_at: "2026-08-13T12:15:00Z",
          },
        } as never;
      }
      return {
        items: [person(1, "Ada")],
        approximate_total: 1253,
      } as never;
    });

    renderPage("/admin/organization/people?query=ada&status=active&sort=email");
    await screen.findAllByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Select all 1,253 results" }));

    expect(await screen.findByText("1,250 people selected")).toBeInTheDocument();
    const selectionCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/organization/people/selections");
    expect(JSON.parse(String(selectionCall?.[1]?.body))).toEqual({
      query: "ada",
      status: ["active"],
      group_ids: [],
      sort: "email",
    });
    expect(String(selectionCall?.[1]?.body)).not.toContain("account_id");

    fireEvent.change(screen.getByRole("combobox", { name: "Membership status" }), {
      target: { value: "suspended" },
    });
    await waitFor(() =>
      expect(screen.queryByText("1,250 people selected")).not.toBeInTheDocument(),
    );
  });

  it("uses the current revision for inline profile group changes", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") {
        return {
          groups: [
            { id: 2, name: "Adults" },
            { id: 3, name: "Kids" },
          ],
        } as never;
      }
      if (String(path).endsWith("/profiles/profile-1") && init?.method === "PATCH") {
        return { person: person(1, "Ada") } as never;
      }
      return { items: [person(1, "Ada")], approximate_total: 1 } as never;
    });

    renderPage();
    expect(
      await screen.findByRole("columnheader", { name: "Security revision" }),
    ).toBeInTheDocument();
    const [group] = await screen.findAllByRole("combobox", { name: "Group for Ada profile" });
    fireEvent.change(group!, { target: { value: "3" } });

    await waitFor(() => {
      const call = vi
        .mocked(adminV2Api)
        .mock.calls.find(([path]) => String(path).endsWith("/profiles/profile-1"));
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        expected_revision: 4,
        group_id: 3,
      });
    });
  });

  it("surfaces a stale profile mutation, reloads the person, and preserves the intended group", async () => {
    const refreshed = { ...person(1, "Ada"), security_revision: 5 };
    let patchCount = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") {
        return {
          groups: [
            { id: 2, name: "Adults" },
            { id: 3, name: "Kids" },
          ],
        } as never;
      }
      if (String(path).endsWith("/profiles/profile-1") && init?.method === "PATCH") {
        patchCount += 1;
        if (patchCount === 1) {
          throw new AdminV2ClientError(
            409,
            "authorization_state_changed",
            "Authorization state changed; reload and retry",
          );
        }
        return { person: refreshed } as never;
      }
      if (path === "/organization/people/1") return { person: refreshed } as never;
      return { items: [person(1, "Ada")], approximate_total: 1 } as never;
    });

    renderPage();
    const [group] = await screen.findAllByRole("combobox", { name: "Group for Ada profile" });
    fireEvent.change(group!, { target: { value: "3" } });

    expect(await screen.findByRole("alert")).toHaveTextContent(/changed on the server/i);
    expect(screen.getByRole("alert")).toHaveTextContent(/Kids/);
    expect(screen.getByRole("alert")).toHaveTextContent(/Adults/);
    expect(group).toHaveValue("3");
    expect(
      vi.mocked(adminV2Api).mock.calls.some(([path]) => path === "/organization/people/1"),
    ).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Retry group change" }));
    await waitFor(() => expect(patchCount).toBe(2));
    const patches = vi
      .mocked(adminV2Api)
      .mock.calls.filter(([path]) => String(path).endsWith("/profiles/profile-1"));
    expect(JSON.parse(String(patches[1]?.[1]?.body))).toEqual({
      expected_revision: 5,
      group_id: 3,
    });
  });

  it("keeps the selected destination group in confirmation and submission", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") {
        return { groups: [{ id: 3, name: "Kids" }] } as never;
      }
      if (path === "/organization/people/selections") {
        return {
          selection: { token: "opaque", matched: 2, excluded: 0, expires_at: "later" },
        } as never;
      }
      if (path === "/organization/people/bulk-jobs" && init?.method === "POST") {
        return {
          job: {
            job_id: "job",
            status: "queued",
            progress_current: 0,
            progress_total: 2,
            succeeded: 0,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      return { items: [person(1, "Ada")], approximate_total: 2 } as never;
    });

    renderPage();
    await screen.findAllByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Select all 2 results" }));
    await screen.findByText("2 people selected");
    fireEvent.change(screen.getByRole("combobox", { name: "Assign selected people to group" }), {
      target: { value: "3" },
    });

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/Assign.*Kids/i)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Start bulk job" }));
    await waitFor(() => {
      const call = vi
        .mocked(adminV2Api)
        .mock.calls.find(([path]) => path === "/organization/people/bulk-jobs");
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({
        selection_token: "opaque",
        kind: "assign_group",
        group_id: 3,
      });
    });
  });

  it("keeps real cursor history across three pages and resets it for browser filter navigation", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      const url = String(path);
      if (url === "/organization/groups") return { groups: [] } as never;
      if (url.includes("cursor=page-two")) {
        return {
          items: [person(2, "Bea")],
          next_cursor: "page-three",
          approximate_total: 3,
        } as never;
      }
      if (url.includes("cursor=page-three")) {
        return { items: [person(3, "Cara")], approximate_total: 3 } as never;
      }
      return {
        items: [person(1, "Ada")],
        next_cursor: "page-two",
        approximate_total: 3,
      } as never;
    });

    renderPage();
    await screen.findAllByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findAllByText("Bea");
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findAllByText("Cara");
    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    expect((await screen.findAllByText("Bea")).length).toBeGreaterThan(0);
    expect(screen.getByLabelText("current location")).toHaveTextContent("cursor=page-two");

    fireEvent.change(screen.getByRole("searchbox", { name: "Search people" }), {
      target: { value: "new" },
    });
    fireEvent.submit(screen.getByRole("search"));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Previous page" })).toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Browser back" }));
    await waitFor(() =>
      expect(screen.getByRole("searchbox", { name: "Search people" })).toHaveValue(""),
    );
  });

  it("includes group identity and recent activity in selection and confirmation", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/groups") {
        return { groups: [{ id: 3, name: "Kids" }] } as never;
      }
      if (path === "/organization/people/selections") {
        return {
          selection: { token: "opaque", matched: 2, excluded: 0, expires_at: "later" },
        } as never;
      }
      return { items: [person(1, "Ada")], approximate_total: 2 } as never;
    });

    renderPage();
    await screen.findAllByText("Ada");
    fireEvent.change(screen.getByRole("combobox", { name: "Access group" }), {
      target: { value: "3" },
    });
    fireEvent.change(screen.getByLabelText("Recent activity since"), {
      target: { value: "2026-08-01" },
    });
    fireEvent.click(await screen.findByRole("button", { name: "Select all 2 results" }));
    await screen.findByText("2 people selected");
    fireEvent.click(screen.getByRole("button", { name: "Suspend memberships" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/group Kids/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/active since 2026-08-01/i)).toBeInTheDocument();
    const selectionCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/organization/people/selections");
    expect(JSON.parse(String(selectionCall?.[1]?.body))).toMatchObject({
      group_ids: [3],
      active_since: "2026-08-01T00:00:00.000Z",
    });
  });

  it("refreshes people after a bulk job reaches a terminal state", async () => {
    let peopleLoads = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") return { groups: [] } as never;
      if (String(path).startsWith("/organization/people?") && !init?.method) {
        peopleLoads += 1;
        return { items: [person(1, "Ada")], approximate_total: 1 } as never;
      }
      if (path === "/organization/people/selections") {
        return {
          selection: { token: "opaque", matched: 1, excluded: 0, expires_at: "later" },
        } as never;
      }
      if (path === "/organization/people/bulk-jobs") {
        return {
          job: {
            job_id: "job-1",
            status: "queued",
            progress_current: 0,
            progress_total: 1,
            succeeded: 0,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/organization/people/bulk-jobs/job-1") {
        return {
          job: {
            job_id: "job-1",
            status: "completed",
            progress_current: 1,
            progress_total: 1,
            succeeded: 1,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      throw new Error(`unexpected ${path}`);
    });

    renderPage();
    await screen.findAllByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Select all 1 results" }));
    await screen.findByText("1 people selected");
    fireEvent.click(screen.getByRole("button", { name: "Suspend memberships" }));
    fireEvent.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Start bulk job",
      }),
    );

    await screen.findByText("Bulk job completed");
    await waitFor(() => expect(peopleLoads).toBeGreaterThan(1));
  });

  it("shows exact partial bulk results without a generic success message", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups") return { groups: [] } as never;
      if (path === "/organization/people/selections") {
        return {
          selection: {
            token: "opaque",
            matched: 3,
            excluded: 1,
            expires_at: "2026-08-13T12:15:00Z",
          },
        } as never;
      }
      if (path === "/organization/people/bulk-jobs" && init?.method === "POST") {
        return {
          job: {
            job_id: "job-1",
            status: "completed",
            progress_current: 3,
            progress_total: 3,
            succeeded: 1,
            skipped: [{ account_id: 8, reason: "protected_owner" }],
            failed: [{ account_id: 9, reason: "authorization_state_changed" }],
          },
        } as never;
      }
      return { items: [person(1, "Ada")], approximate_total: 4 } as never;
    });

    renderPage();
    await screen.findAllByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Select all 4 results" }));
    await screen.findByText("3 people selected");
    fireEvent.click(screen.getByRole("button", { name: "Suspend memberships" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/North Sea Media/)).toBeInTheDocument();
    expect(within(dialog).getByText(/3 matched/)).toBeInTheDocument();
    expect(within(dialog).getByText(/1 excluded/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Start bulk job" }));

    expect(await screen.findByText("1 succeeded")).toBeInTheDocument();
    expect(screen.getByText("Account 8 — protected owner")).toBeInTheDocument();
    expect(screen.getByText("Account 9 — authorization state changed")).toBeInTheDocument();
    expect(screen.queryByText(/^Success$/i)).not.toBeInTheDocument();
  });
});
