// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import type { EntitlementCohort, PolicyPreview } from "@/hooks/queries/admin/entitlementCohorts";
import BulkPolicyDrawer from "./BulkPolicyDrawer";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const standard: EntitlementCohort = {
  cohort_id: "cohort-standard",
  organization_id: "org-1",
  name: "Standard",
  revision: 2,
  access_group_id: 12,
  source_template_key: "standard",
  source_template_revision: 4,
  derivation_kind: "exact_template",
  policy_digest: "digest-standard",
  member_count: 85,
  archived: false,
  created_by_account_id: 7,
  created_at: "2026-08-20T10:00:00Z",
  policy: {
    library_ids: [1, 2],
    playback_allowed: true,
    max_streams: 2,
    max_profiles: 5,
    transcode_allowed: true,
    max_transcodes: 1,
    download_allowed: false,
    download_transcode_allowed: false,
    max_playback_quality: "1080p",
    allowed_permissions: ["request_media"],
    requests_allowed: true,
  },
};

const preview: PolicyPreview = {
  matched: 120,
  excluded: 3,
  already_compliant: 14,
  inherited_profiles_will_move: 172,
  custom_profiles_will_remain: 9,
  custom_profiles_will_move: 0,
  ineligible_or_stale: 2,
  current_cohorts: [
    {
      group_id: 9,
      group_name: "Browse access",
      cohort_id: "cohort-old",
      cohort_revision: 1,
      source_template_key: "browse",
      source_template_revision: 1,
      state: "managed",
      count: 106,
    },
  ],
  target: {
    kind: "assign_entitlement_cohort",
    cohort_id: standard.cohort_id,
    cohort_revision: standard.revision,
    group_id: standard.access_group_id,
    template_key: standard.source_template_key,
    template_revision: standard.source_template_revision,
    name: standard.name,
    policy_digest: standard.policy_digest,
    policy: standard.policy,
  },
  diff: [
    { field: "max_streams", changed_accounts: 90 },
    { field: "download_allowed", changed_accounts: 106 },
  ],
  selection_expires_at: "2026-08-22T13:00:00Z",
  confirmation_expires_at: "2026-08-22T12:15:00Z",
  confirmation_token: "signed-confirmation",
};

function renderDrawer(props: Partial<React.ComponentProps<typeof BulkPolicyDrawer>> = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <BulkPolicyDrawer
        open
        contextKey="organization:org-1"
        organizationName="North Sea Media"
        selection={{
          token: "signed-selection",
          matched: 120,
          excluded: 3,
          expires_at: "2026-08-22T13:00:00Z",
        }}
        cohorts={[standard]}
        onOpenChange={vi.fn()}
        onRetrySelection={vi.fn()}
        {...props}
      />
    </QueryClientProvider>,
  );
}

const libraries = {
  libraries: [
    { folder_id: 8, name: "Kids", type: "movies", access_kind: "owned" },
    { folder_id: 13, name: "Documentaries", type: "movies", access_kind: "owned" },
  ],
};

function previewCalls() {
  return vi
    .mocked(adminV2Api)
    .mock.calls.filter(([path]) => path === "/organization/people/policy-previews");
}

async function goToOperationStep() {
  expect(screen.getByRole("heading", { name: "Check the accounts" })).toBeInTheDocument();
  expect(screen.getByText("Selected").nextSibling).toHaveTextContent("120");
  expect(screen.getByText("Left out by your filters").nextSibling).toHaveTextContent("3");
  await userEvent.click(screen.getByRole("button", { name: "Next" }));
}

describe("BulkPolicyDrawer", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("offers every change, library and permission pickers, a live summary, and dependency-safe controls", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/organization/libraries") return libraries as never;
      throw new Error(`unexpected ${path}`);
    });
    renderDrawer();
    await goToOperationStep();

    expect(screen.getByRole("radiogroup", { name: "What should change" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Move to a cohort" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Put on a plan" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Change some settings" })).toBeInTheDocument();
    expect(
      screen.getByRole("radio", { name: "Reset to the organization default" }),
    ).toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Change some settings" }));
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Start from cohort" }),
      standard.cohort_id,
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name for the new cohort" }),
      "Browse only",
    );
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Libraries" }), "add");
    await userEvent.click(await screen.findByRole("checkbox", { name: "Kids" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Documentaries" }));
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Extra permissions" }),
      "remove",
    );
    await userEvent.click(screen.getByRole("checkbox", { name: "Edit metadata" }));
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Plays video" }), "false");

    expect(screen.getByRole("spinbutton", { name: "Screens at once" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Video conversion" })).toBeDisabled();
    const diff = screen.getByRole("region", { name: "Your changes" });
    expect(diff).toHaveTextContent(/Libraries: Add Kids, Documentaries/);
    expect(diff).toHaveTextContent(/Extra permissions: Remove Edit metadata/);
    expect(diff).toHaveTextContent(/Plays video: not allowed/);
  });

  it("uses the server preview, re-previews after the custom-profile option changes, and guards enqueue", async () => {
    let jobCalls = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/people/policy-previews" && init?.method === "POST") {
        const body = JSON.parse(String(init.body));
        return {
          preview: {
            ...preview,
            custom_profiles_will_remain: body.command.include_custom_profiles ? 0 : 9,
            custom_profiles_will_move: body.command.include_custom_profiles ? 9 : 0,
          },
        } as never;
      }
      if (path === "/organization/people/policy-jobs" && init?.method === "POST") {
        jobCalls += 1;
        await Promise.resolve();
        return {
          job: {
            job_id: "policy-job-1",
            status: "queued",
            progress_current: 0,
            progress_total: 120,
            succeeded: 0,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/organization/people/policy-jobs/policy-job-1") {
        return {
          job: {
            job_id: "policy-job-1",
            status: "running",
            progress_current: 48,
            progress_total: 120,
            succeeded: 47,
            skipped: [{ account_id: 18, reason: "membership_state_changed" }],
            failed: [],
          },
        } as never;
      }
      if (path === "/organization/libraries") return libraries as never;
      throw new Error(`unexpected ${path}`);
    });

    renderDrawer();
    await goToOperationStep();
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));

    expect(await screen.findByRole("heading", { name: "Review the change" })).toBeInTheDocument();
    expect(screen.getByText("172 that follow their account change with it")).toBeInTheDocument();
    expect(screen.getByText("9 with their own settings stay as they are")).toBeInTheDocument();
    expect(screen.getByText("Screens at once for 90 accounts")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Where they are now" })).toHaveTextContent(
      /106 accounts: plan browse, revision 1/,
    );
    const target = screen.getByRole("region", { name: "What they'll get" });
    expect(target).toHaveTextContent(/Plan standard, revision 4/);
    expect(target).toHaveTextContent(/digest-standard/);
    expect(screen.getByRole("region", { name: "New settings" })).toHaveTextContent(
      /Screens at once\s*2 screens/,
    );

    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    await userEvent.click(
      screen.getByRole("checkbox", { name: /Also change profiles with their own settings/ }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByText("9 with their own settings change too")).toBeInTheDocument();
    expect(previewCalls()).toHaveLength(2);

    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByRole("heading", { name: "Confirm" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "What they'll get" })).toHaveTextContent(
      /standard.*digest-standard/,
    );
    expect(screen.getByRole("button", { name: "Apply to 120 accounts" })).toBeDisabled();
    await userEvent.click(
      screen.getByRole("checkbox", { name: /checked this change for 120 accounts/ }),
    );
    const start = screen.getByRole("button", { name: "Apply to 120 accounts" });
    fireEvent.click(start);
    fireEvent.click(start);

    expect(await screen.findByRole("heading", { name: "Applying the change" })).toBeInTheDocument();
    expect(jobCalls).toBe(1);
    const call = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/organization/people/policy-jobs");
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
      selection_token: "signed-selection",
      confirmation_token: "signed-confirmation",
      command: {
        kind: "assign_entitlement_cohort",
        cohort_id: standard.cohort_id,
        include_custom_profiles: true,
      },
    });
    expect(JSON.parse(String(call?.[1]?.body)).idempotency_key).toEqual(expect.any(String));
  });

  it("normalizes disabled policy dependencies before previewing a derived cohort", async () => {
    let requestBody: Record<string, unknown> | undefined;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/people/policy-previews") {
        requestBody = JSON.parse(String(init?.body));
        return { preview } as never;
      }
      if (path === "/organization/libraries") return libraries as never;
      throw new Error(`unexpected ${path}`);
    });

    renderDrawer();
    await goToOperationStep();
    await userEvent.click(screen.getByRole("radio", { name: "Change some settings" }));
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name for the new cohort" }),
      "No playback",
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Video conversion" }),
      "true",
    );
    await userEvent.type(screen.getByRole("spinbutton", { name: "Conversions at once" }), "3");
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Download original files" }),
      "true",
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Download converted copies" }),
      "true",
    );
    await userEvent.type(screen.getByRole("spinbutton", { name: "Screens at once" }), "4");
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Best picture quality" }),
      "2160p",
    );
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Plays video" }), "false");
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));

    await waitFor(() => expect(requestBody).toBeDefined());
    expect(requestBody).toMatchObject({
      command: {
        patch: {
          playback_allowed: false,
          max_streams: 0,
          transcode_allowed: false,
          max_transcodes: 0,
          download_allowed: false,
          download_transcode_allowed: false,
        },
      },
    });
    expect((requestBody?.command as { patch: Record<string, unknown> }).patch).toHaveProperty(
      "max_playback_quality",
      "",
    );
  });

  it("rejects a deep-linked cohort that is absent from the loaded active cohort list", async () => {
    vi.mocked(adminV2Api).mockResolvedValue({ preview } as never);
    renderDrawer({ initialCohortID: "archived-cohort" });
    await goToOperationStep();

    expect(screen.getByRole("combobox", { name: "Cohort" })).toHaveValue("");
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(screen.getByRole("alert")).toHaveTextContent(/Choose a cohort/);
    expect(previewCalls()).toHaveLength(0);
  });

  it("asks for at least one library before previewing a library change", async () => {
    vi.mocked(adminV2Api).mockResolvedValue(libraries as never);
    renderDrawer();
    await goToOperationStep();
    await userEvent.click(screen.getByRole("radio", { name: "Change some settings" }));
    await userEvent.type(
      screen.getByRole("textbox", { name: "Name for the new cohort" }),
      "Fewer libraries",
    );
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Libraries" }), "add");
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));

    expect(screen.getByRole("alert")).toHaveTextContent(/Pick at least one library/);
    expect(previewCalls()).toHaveLength(0);
  });

  it("shows progress, stops queued work, and never exposes an unexpected failure", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/people/policy-previews") return { preview } as never;
      if (path === "/organization/people/policy-jobs") {
        return {
          job: {
            job_id: "policy-job-2",
            status: "queued",
            progress_current: 0,
            progress_total: 120,
            succeeded: 0,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/organization/people/policy-jobs/policy-job-2" && !init?.method) {
        return {
          job: {
            job_id: "policy-job-2",
            status: "running",
            progress_current: 60,
            progress_total: 120,
            succeeded: 59,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/organization/people/policy-jobs/policy-job-2/cancel") {
        return {
          job: {
            job_id: "policy-job-2",
            status: "cancelled",
            progress_current: 60,
            progress_total: 120,
            succeeded: 59,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      throw new Error("database password=never-display-this");
    });

    renderDrawer();
    await goToOperationStep();
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByRole("heading", { name: "Review the change" });
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    await userEvent.click(
      screen.getByRole("checkbox", { name: /checked this change for 120 accounts/ }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Apply to 120 accounts" }));

    expect(await screen.findByRole("progressbar", { name: "Progress" })).toHaveAttribute(
      "aria-valuenow",
      "50",
    );
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(await screen.findByRole("heading", { name: "Change stopped" })).toBeInTheDocument();
    expect(screen.queryByText(/never-display-this/i)).not.toBeInTheDocument();
  });

  it("offers a fresh selection for stale confirmations and exposes keyboard labels", async () => {
    const retrySelection = vi.fn();
    vi.mocked(adminV2Api).mockRejectedValue(
      new AdminV2ClientError(
        409,
        "policy_confirmation_stale",
        "The policy preview changed or expired; create a new preview",
      ),
    );
    renderDrawer({ onRetrySelection: retrySelection });

    expect(screen.getByRole("dialog", { name: "Change plan or settings" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Steps" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
    await goToOperationStep();
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/Something changed since the preview/);
    await userEvent.click(screen.getByRole("button", { name: "Review the accounts again" }));
    expect(retrySelection).toHaveBeenCalledOnce();
  });
});
