// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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

async function goToOperationStep() {
  expect(screen.getByRole("heading", { name: "Review selection" })).toBeInTheDocument();
  expect(screen.getByText("120 matched")).toBeInTheDocument();
  expect(screen.getByText("3 excluded")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Choose policy operation" }));
}

describe("BulkPolicyDrawer", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("offers every operation, explicit set modes, a live draft diff, and dependency-safe controls", async () => {
    renderDrawer();
    await goToOperationStep();

    expect(screen.getByRole("radiogroup", { name: "Policy operation" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Move to an existing cohort" })).toBeInTheDocument();
    expect(
      screen.getByRole("radio", { name: "Apply an exact template revision" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("radio", { name: "Derive a policy for this selection" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Restore the managed default" })).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("radio", { name: "Derive a policy for this selection" }),
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Base cohort" }),
      standard.cohort_id,
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Derived cohort name" }),
      "Browse only",
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Library operation" }),
      "add",
    );
    await userEvent.type(screen.getByRole("textbox", { name: "Library IDs" }), "8, 13");
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Permission operation" }),
      "remove",
    );
    await userEvent.type(screen.getByRole("textbox", { name: "Permissions" }), "request_media");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Playback" }), "false");

    expect(screen.getByRole("spinbutton", { name: "Maximum streams" })).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Transcoding" })).toBeDisabled();
    const diff = screen.getByRole("region", { name: "Draft policy changes" });
    expect(diff).toHaveTextContent(/libraries: add 8, 13/i);
    expect(diff).toHaveTextContent(/permissions: remove request_media/i);
    expect(diff).toHaveTextContent(/playback: disabled/i);
  });

  it("uses the authoritative preview, resets it after custom-profile changes, and synchronously guards enqueue", async () => {
    let previewCalls = 0;
    let jobCalls = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/organization/people/policy-previews" && init?.method === "POST") {
        previewCalls += 1;
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
      throw new Error(`unexpected ${path}`);
    });

    renderDrawer();
    await goToOperationStep();
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Target cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview policy impact" }));

    expect(await screen.findByRole("heading", { name: "Review impact" })).toBeInTheDocument();
    expect(screen.getByText("172 inherited profiles move")).toBeInTheDocument();
    expect(screen.getByText("9 custom profiles remain unchanged")).toBeInTheDocument();
    expect(screen.getByText("90 accounts change maximum streams")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Authoritative target policy" })).toHaveTextContent(
      /Maximum streams.*2/i,
    );

    await userEvent.click(screen.getByRole("checkbox", { name: "Move custom profiles too" }));
    expect(screen.queryByText("172 inherited profiles move")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/preview is out of date/i);
    await userEvent.click(screen.getByRole("button", { name: "Recalculate policy impact" }));
    expect(await screen.findByText("9 custom profiles move")).toBeInTheDocument();
    expect(previewCalls).toBe(2);

    await userEvent.click(screen.getByRole("button", { name: "Continue to confirmation" }));
    expect(screen.getByRole("heading", { name: "Confirm policy job" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start policy job" })).toBeDisabled();
    await userEvent.click(
      screen.getByRole("checkbox", { name: "I understand this creates a durable policy job" }),
    );
    const start = screen.getByRole("button", { name: "Start policy job" });
    fireEvent.click(start);
    fireEvent.click(start);

    expect(await screen.findByRole("heading", { name: "Policy job running" })).toBeInTheDocument();
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

  it("shows progress, cancels queued work, and never exposes an unexpected failure", async () => {
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
      screen.getByRole("combobox", { name: "Target cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview policy impact" }));
    await userEvent.click(await screen.findByRole("button", { name: "Continue to confirmation" }));
    await userEvent.click(
      screen.getByRole("checkbox", { name: "I understand this creates a durable policy job" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Start policy job" }));

    expect(await screen.findByRole("progressbar", { name: "Policy job progress" })).toHaveAttribute(
      "aria-valuenow",
      "50",
    );
    await userEvent.click(screen.getByRole("button", { name: "Cancel policy job" }));
    expect(
      await screen.findByRole("heading", { name: "Policy job cancelled" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/never-display-this/i)).not.toBeInTheDocument();
  });

  it("offers a fresh immutable selection for stale confirmations and exposes keyboard labels", async () => {
    const retrySelection = vi.fn();
    vi.mocked(adminV2Api).mockRejectedValue(
      new AdminV2ClientError(
        409,
        "policy_confirmation_stale",
        "The policy preview changed or expired; create a new preview",
      ),
    );
    renderDrawer({ onRetrySelection: retrySelection });

    expect(screen.getByRole("dialog", { name: "Apply entitlement policy" })).toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Policy workflow steps" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
    await goToOperationStep();
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Target cohort" }),
      standard.cohort_id,
    );
    await userEvent.click(screen.getByRole("button", { name: "Preview policy impact" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/preview changed or expired/i);
    await userEvent.click(screen.getByRole("button", { name: "Refresh selection and retry" }));
    expect(retrySelection).toHaveBeenCalledOnce();
  });
});
