// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import AdminAccessGroups from "./AdminAccessGroups";

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

const defaultGroup = {
  id: 1,
  name: "Default",
  description: "",
  library_ids: null,
  max_playback_quality: "source",
  download_allowed: true,
  download_transcode_allowed: true,
  max_streams: 0,
  max_transcodes: 0,
  allowed_permissions: null,
  requests_allowed: true,
  is_default: true,
  member_count: 8,
  created_at: "2026-07-02T12:00:00Z",
  updated_at: "2026-07-02T12:00:00Z",
};

const kidsGroup = {
  ...defaultGroup,
  id: 2,
  name: "Kids",
  library_ids: [4],
  is_default: false,
  member_count: 4,
};

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AdminAccessGroups />
    </QueryClientProvider>,
  );
}

describe("AdminAccessGroups organization context", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("uses only organization v2 resources and labels the active organization", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/groups") {
        return { groups: [defaultGroup, kidsGroup] } as never;
      }
      if (path === "/organization/libraries") {
        return {
          libraries: [
            { folder_id: 4, name: "Family Movies", type: "movies", access_kind: "owned" },
          ],
        } as never;
      }
      throw new Error(`unexpected ${path}`);
    });

    renderPage();

    expect(await screen.findByText("Kids")).toBeInTheDocument();
    expect(screen.getByText(/North Sea Media/)).toBeInTheDocument();
    expect(vi.mocked(adminV2Api).mock.calls.map(([path]) => path)).toContain(
      "/organization/groups",
    );
    expect(
      vi.mocked(adminV2Api).mock.calls.some(([path]) => String(path).includes("/api/v1")),
    ).toBe(false);
  });

  it("previews same-organization default-group reassignment before delete", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/groups" && !init?.method) {
        return { groups: [defaultGroup, kidsGroup] } as never;
      }
      if (path === "/organization/libraries") return { libraries: [] } as never;
      if (path === "/organization/groups/2" && init?.method === "DELETE") {
        return { profiles_reassigned: 4, default_group_id: 1 } as never;
      }
      throw new Error(`unexpected ${path}`);
    });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Kids/ }));
    fireEvent.click(screen.getByRole("button", { name: "Delete group" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/4 profiles/)).toBeInTheDocument();
    expect(within(dialog).getByText(/Default/)).toBeInTheDocument();
    expect(within(dialog).getByText(/North Sea Media/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      const call = vi
        .mocked(adminV2Api)
        .mock.calls.find(([path, init]) => path === "/organization/groups/2" && init?.method);
      expect(call?.[1]?.method).toBe("DELETE");
      expect(JSON.parse(String(call?.[1]?.body))).toEqual({ expected_revision: 7 });
    });
  });
});
