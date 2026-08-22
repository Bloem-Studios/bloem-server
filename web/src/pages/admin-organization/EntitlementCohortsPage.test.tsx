// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import EntitlementCohortsPage from "./EntitlementCohortsPage";

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
    },
  }),
}));

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <EntitlementCohortsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("EntitlementCohortsPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("shows immutable lineage, membership, archive state, and the complete observed policy", async () => {
    vi.mocked(adminV2Api).mockResolvedValue({
      cohorts: [
        {
          cohort_id: "cohort-child",
          organization_id: "org-1",
          name: "Low bandwidth",
          revision: 1,
          access_group_id: 22,
          source_template_key: "standard",
          source_template_revision: 4,
          parent_cohort_id: "cohort-parent",
          derivation_kind: "policy_patch",
          policy_digest: "safe-policy-digest",
          member_count: 31,
          archived: true,
          created_by_account_id: 7,
          created_at: "2026-08-20T10:00:00Z",
          policy: {
            library_ids: null,
            playback_allowed: true,
            max_streams: 1,
            max_profiles: 4,
            transcode_allowed: true,
            max_transcodes: 1,
            download_allowed: false,
            download_transcode_allowed: false,
            max_playback_quality: "720p",
            allowed_permissions: null,
            requests_allowed: true,
          },
        },
      ],
    } as never);

    renderPage();

    const cohort = await screen.findByRole("article", { name: "Low bandwidth revision 1" });
    expect(within(cohort).getByText("Archived")).toBeInTheDocument();
    expect(within(cohort).getByText("31 people")).toBeInTheDocument();
    expect(within(cohort).getByText(/standard.*revision 4/i)).toBeInTheDocument();
    expect(within(cohort).getByText(/Derived from.*cohort-parent/i)).toBeInTheDocument();
    expect(within(cohort).getByText(/Created by account 7/i)).toBeInTheDocument();
    const policy = within(cohort).getByRole("region", { name: "Effective policy" });
    expect(policy).toHaveTextContent(/Libraries.*All libraries/i);
    expect(policy).toHaveTextContent(/Playback.*Allowed/i);
    expect(policy).toHaveTextContent(/Maximum streams.*1/i);
    expect(policy).toHaveTextContent(/Maximum profiles.*4/i);
    expect(policy).toHaveTextContent(/Transcoding.*Allowed/i);
    expect(policy).toHaveTextContent(/Maximum transcodes.*1/i);
    expect(policy).toHaveTextContent(/Downloads.*Not allowed/i);
    expect(policy).toHaveTextContent(/Transcoded downloads.*Not allowed/i);
    expect(policy).toHaveTextContent(/Maximum playback quality.*720p/i);
    expect(policy).toHaveTextContent(/Permissions.*Unrestricted/i);
    expect(policy).toHaveTextContent(/Requests.*Allowed/i);
    expect(
      within(cohort).queryByRole("link", { name: "Apply Low bandwidth to people" }),
    ).not.toBeInTheDocument();
    expect(within(cohort).queryByRole("button", { name: /edit/i })).not.toBeInTheDocument();
    expect(vi.mocked(adminV2Api)).toHaveBeenCalledWith(
      "/organization/entitlement-cohorts?include_archived=true",
    );
  });

  it("renders safe loading, empty, and error states", async () => {
    vi.mocked(adminV2Api).mockResolvedValueOnce({ cohorts: [] } as never);
    const first = renderPage();
    expect(
      await screen.findByText(/No policy cohorts have been materialized/i),
    ).toBeInTheDocument();
    first.unmount();
    vi.mocked(adminV2Api).mockRejectedValueOnce(
      new AdminV2ClientError(503, "entitlements_unavailable", "Cohorts unavailable"),
    );
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Cohorts unavailable");
  });
});
