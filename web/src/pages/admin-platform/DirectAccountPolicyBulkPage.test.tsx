// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import DirectAccountPolicyBulkPage from "./DirectAccountPolicyBulkPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

const organizationID = "10000000-0000-0000-0000-000000000001";

const policy = (overrides: Record<string, unknown> = {}) => ({
  library_ids: [12, 18],
  playback_allowed: true,
  max_streams: 2,
  max_profiles: 4,
  transcode_allowed: true,
  audio_transcode_allowed: true,
  max_transcodes: 1,
  download_allowed: false,
  download_transcode_allowed: false,
  max_playback_quality: "1080p",
  allowed_permissions: ["request_media"],
  requests_allowed: true,
  ...overrides,
});

const snapshot = (
  accountID: number,
  state: "managed" | "custom" | "legacy_unmanaged" = "managed",
) => ({
  observed_at: "2026-08-21T10:15:00Z",
  organization_id: organizationID,
  account_id: accountID,
  group_id: 100 + accountID,
  ...(state === "managed"
    ? {
        cohort_id: `20000000-0000-0000-0000-${String(accountID).padStart(12, "0")}`,
        cohort_revision: 3,
        source_template_key: "premium",
        source_template_revision: 7,
      }
    : {}),
  state,
  policy_revision: 8,
  policy: policy(),
  profiles: [
    {
      profile_id: `profile-${accountID}-inherited`,
      profile_name: `Inherited ${accountID}`,
      group_id: 100 + accountID,
      inherits_account: true,
      state,
      policy: policy(),
    },
    {
      profile_id: `profile-${accountID}-custom`,
      profile_name: `Custom ${accountID}`,
      group_id: 900 + accountID,
      inherits_account: false,
      state: "legacy_unmanaged",
      policy: policy({ download_allowed: true, max_streams: 1 }),
    },
  ],
});

const cohorts = [
  {
    cohort_id: "30000000-0000-0000-0000-000000000001",
    organization_id: organizationID,
    name: "Standard",
    revision: 2,
    access_group_id: 301,
    source_template_key: "standard",
    source_template_revision: 4,
    derivation_kind: "exact_template",
    policy: policy({ max_streams: 1 }),
    policy_digest: "digest-standard",
    member_count: 12,
    archived: false,
    created_at: "2026-08-20T10:00:00Z",
  },
  {
    cohort_id: "30000000-0000-0000-0000-000000000002",
    organization_id: organizationID,
    name: "Premium",
    revision: 5,
    access_group_id: 302,
    source_template_key: "premium",
    source_template_revision: 7,
    derivation_kind: "exact_template",
    policy: policy({ max_streams: 4 }),
    policy_digest: "digest-premium",
    member_count: 6,
    archived: false,
    created_at: "2026-08-20T11:00:00Z",
  },
];

function previewResponse(commandKind: string, overrides: Record<string, unknown> = {}) {
  return {
    selection: {
      token: "signed-direct-selection",
      matched: 2,
      excluded: 0,
      expires_at: "2026-08-21T11:15:00Z",
    },
    preview: {
      matched: 2,
      excluded: 0,
      already_compliant: 0,
      inherited_profiles_will_move: 2,
      custom_profiles_will_remain: 2,
      custom_profiles_will_move: 0,
      ineligible_or_stale: 0,
      current_cohorts: [
        {
          group_id: 141,
          group_name: "Current",
          state: "managed",
          count: 1,
          source_template_key: "premium",
          source_template_revision: 7,
        },
        { group_id: 142, group_name: "Legacy", state: "legacy_unmanaged", count: 1 },
      ],
      target: {
        kind: commandKind,
        cohort_id: cohorts[0]!.cohort_id,
        cohort_revision: 2,
        group_id: 301,
        template_key: "standard",
        template_revision: 4,
        name: "Download cohort",
        policy_digest: "digest-target",
        policy: policy({ download_allowed: true }),
      },
      diff: [{ field: "download_allowed", changed_accounts: 2 }],
      selection_expires_at: "2026-08-21T11:15:00Z",
      confirmation_expires_at: "2026-08-21T10:30:00Z",
      confirmation_token: "signed-confirmation",
      ...overrides,
    },
  };
}

function installBaseRoutes(options: { snapshotItems?: unknown[] } = {}) {
  vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
    if (path === "/platform/accounts/entitlement-snapshots" && init?.method === "POST") {
      return {
        observed_at: "2026-08-21T10:15:00Z",
        items: options.snapshotItems ?? [
          { account_id: 41, snapshot: snapshot(41) },
          { account_id: 42, snapshot: snapshot(42, "custom") },
        ],
      } as never;
    }
    if (
      path ===
      `/platform/organizations/${organizationID}/entitlement-cohorts?include_archived=false`
    ) {
      return { cohorts } as never;
    }
    throw new Error(`Unexpected API route: ${String(path)}`);
  });
}

function snapshotCalls() {
  return vi
    .mocked(adminV2Api)
    .mock.calls.filter(([path]) => path === "/platform/accounts/entitlement-snapshots");
}

function previewCalls() {
  return vi
    .mocked(adminV2Api)
    .mock.calls.filter(([path]) => path === "/platform/accounts/entitlement-bulk/policy-previews");
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <DirectAccountPolicyBulkPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function reviewAccounts(user: ReturnType<typeof userEvent.setup>, ids = "41, 42") {
  await user.type(screen.getByLabelText("Account numbers"), ids);
  await user.click(screen.getByRole("button", { name: "Look up current settings" }));
  await screen.findByRole("heading", { name: "Account 41" });
}

describe("DirectAccountPolicyBulkPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("requires whole account numbers before reading any account", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(
      screen.queryByRole("radiogroup", { name: "What should change" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Look up current settings" })).toBeDisabled();
    await user.type(screen.getByLabelText("Account numbers"), "41, 4x");
    await user.click(screen.getByRole("button", { name: "Look up current settings" }));

    expect(screen.getByRole("alert")).toHaveTextContent(/whole numbers/i);
    expect(snapshotCalls()).toHaveLength(0);
  });

  it("reads each account once even when it is listed twice", async () => {
    const user = userEvent.setup();
    installBaseRoutes({ snapshotItems: [{ account_id: 41, snapshot: snapshot(41) }] });
    renderPage();
    await reviewAccounts(user, "41, 41");

    expect(JSON.parse(String(snapshotCalls()[0]?.[1]?.body))).toEqual({ account_ids: [41] });
  });

  it("puts accounts on a chosen plan revision from the plan list", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/platform/entitlement-templates?status=enabled") {
        return {
          templates: [
            { key: "premium", name: "Premium", revision: 7, enabled: true, archived: false },
          ],
        } as never;
      }
      if (path === "/platform/accounts/entitlement-snapshots" && init?.method === "POST") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [{ account_id: 41, snapshot: snapshot(41) }],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return previewResponse("apply_entitlement_template") as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });
    renderPage();
    await reviewAccounts(user, "41");
    await user.click(screen.getByRole("radio", { name: "Put on a plan" }));
    await user.selectOptions(await screen.findByLabelText("Plan"), "premium");
    expect(screen.getByLabelText("Revision")).toHaveValue("7");
    await user.selectOptions(screen.getByLabelText("Revision"), "6");
    await user.click(screen.getByRole("button", { name: "Preview" }));

    await screen.findByRole("heading", { name: "4. Review and apply" });
    expect(JSON.parse(String(previewCalls()[0]?.[1]?.body)).command).toEqual({
      kind: "apply_entitlement_template",
      template_key: "premium",
      template_revision: 6,
      include_custom_profiles: false,
    });
  });

  it("renders complete authoritative account and profile policy before enabling operations", async () => {
    const user = userEvent.setup();
    installBaseRoutes();
    renderPage();
    await reviewAccounts(user);

    const account41 = screen.getByRole("article", { name: "Account 41 current settings" });
    expect(account41).toHaveTextContent("Plan premium, revision 7");
    expect(account41).toHaveTextContent(/Libraries\s*Library 12, Library 18/);
    expect(account41).toHaveTextContent(/Audio conversion\s*Yes/);
    expect(account41).toHaveTextContent("Profiles (2)");
    expect(account41).toHaveTextContent("Custom 41");
    expect(account41).toHaveTextContent("Has its own settings");
    expect(account41).toHaveTextContent("Follows the account");
    const account42 = screen.getByRole("article", { name: "Account 42 current settings" });
    expect(account42).toHaveTextContent("Has its own settings");
    expect(screen.getByText(/^As of/)).toHaveTextContent("2026");

    expect(screen.getByRole("radiogroup", { name: "What should change" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview" })).toBeDisabled();
    expect(screen.getAllByRole("radio").every((radio) => !radio.hasAttribute("checked"))).toBe(
      true,
    );
    expect(screen.queryByRole("combobox", { name: "Cohort" })).not.toBeInTheDocument();
  });

  it("blocks the workflow when an account is outside the authoritative direct-account scope", async () => {
    const user = userEvent.setup();
    installBaseRoutes({
      snapshotItems: [
        { account_id: 41, snapshot: snapshot(41) },
        { account_id: 999, error: "not_found" },
      ],
    });
    renderPage();
    await reviewAccounts(user, "41, 999");

    expect(screen.getByRole("alert")).toHaveTextContent(/Account 999 isn't a direct account/);
    expect(
      screen.queryByRole("radiogroup", { name: "What should change" }),
    ).not.toBeInTheDocument();
  });

  it("discards a reviewed preview as soon as the visible account selection changes", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [
            { account_id: 41, snapshot: snapshot(41) },
            { account_id: 42, snapshot: snapshot(42, "custom") },
          ],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return previewResponse("restore_default_entitlement") as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user);
    await user.click(screen.getByRole("radio", { name: "Reset to the organization default" }));
    await user.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByRole("heading", { name: "4. Review and apply" });
    await user.click(screen.getByRole("checkbox", { name: /checked this change for 2 accounts/ }));

    await user.clear(screen.getByLabelText("Account numbers"));
    await user.type(screen.getByLabelText("Account numbers"), "99");

    expect(screen.queryByRole("heading", { name: "4. Review and apply" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply to 2 accounts" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("radiogroup", { name: "What should change" }),
    ).not.toBeInTheDocument();
  });

  it("does not retain an old reviewed workflow when replacement account IDs are invalid", async () => {
    const user = userEvent.setup();
    installBaseRoutes();
    renderPage();
    await reviewAccounts(user);

    await user.clear(screen.getByLabelText("Account numbers"));
    await user.type(screen.getByLabelText("Account numbers"), "41, invalid");
    await user.click(screen.getByRole("button", { name: "Look up current settings" }));

    expect(screen.getByRole("alert")).toHaveTextContent(/whole numbers/i);
    expect(screen.queryByRole("heading", { name: "2. Current settings" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("radiogroup", { name: "What should change" }),
    ).not.toBeInTheDocument();
  });

  it("ignores a snapshot response that resolves after the account selection changes", async () => {
    const user = userEvent.setup();
    const lateSnapshot = deferred<{
      observed_at: string;
      items: Array<{ account_id: number; snapshot: ReturnType<typeof snapshot> }>;
    }>();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return lateSnapshot.promise as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await user.type(screen.getByLabelText("Account numbers"), "41");
    await user.click(screen.getByRole("button", { name: "Look up current settings" }));
    await user.clear(screen.getByLabelText("Account numbers"));
    await user.type(screen.getByLabelText("Account numbers"), "99");

    await act(async () => {
      lateSnapshot.resolve({
        observed_at: "2026-08-21T10:15:00Z",
        items: [{ account_id: 41, snapshot: snapshot(41) }],
      });
      await lateSnapshot.promise;
    });

    expect(screen.queryByRole("heading", { name: "Account 41" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "2. Current settings" })).not.toBeInTheDocument();
  });

  it("rejects an equal-sized snapshot response for different account IDs", async () => {
    const user = userEvent.setup();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [{ account_id: 51, snapshot: snapshot(51) }],
        } as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await user.type(screen.getByLabelText("Account numbers"), "41");
    await user.click(screen.getByRole("button", { name: "Look up current settings" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/different accounts/);
    expect(screen.queryByRole("heading", { name: "Account 51" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("radiogroup", { name: "What should change" }),
    ).not.toBeInTheDocument();
  });

  it("ignores a preview response that resolves after the account selection changes", async () => {
    const user = userEvent.setup();
    const latePreview = deferred<ReturnType<typeof previewResponse>>();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [{ account_id: 41, snapshot: snapshot(41) }],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return latePreview.promise as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user, "41");
    await user.click(screen.getByRole("radio", { name: "Reset to the organization default" }));
    await user.click(screen.getByRole("button", { name: "Preview" }));
    await user.clear(screen.getByLabelText("Account numbers"));
    await user.type(screen.getByLabelText("Account numbers"), "99");

    await act(async () => {
      latePreview.resolve(previewResponse("restore_default_entitlement"));
      await latePreview.promise;
    });

    expect(screen.queryByRole("heading", { name: "4. Review and apply" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Apply to 2 accounts" })).not.toBeInTheDocument();
  });

  it("ignores a job response that resolves after the account selection changes", async () => {
    const user = userEvent.setup();
    const lateJob = deferred<{
      job: {
        job_id: string;
        status: "completed";
        progress_current: number;
        progress_total: number;
        succeeded: number;
        skipped: never[];
        failed: never[];
      };
    }>();
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [{ account_id: 41, snapshot: snapshot(41) }],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return previewResponse("restore_default_entitlement") as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs") {
        return lateJob.promise as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user, "41");
    await user.click(screen.getByRole("radio", { name: "Reset to the organization default" }));
    await user.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByRole("heading", { name: "4. Review and apply" });
    await user.click(screen.getByRole("checkbox", { name: /checked this change for 2 accounts/ }));
    await user.click(screen.getByRole("button", { name: "Apply to 2 accounts" }));
    await user.clear(screen.getByLabelText("Account numbers"));
    await user.type(screen.getByLabelText("Account numbers"), "99");

    await act(async () => {
      lateJob.resolve({
        job: {
          job_id: "late-job",
          status: "completed",
          progress_current: 1,
          progress_total: 1,
          succeeded: 1,
          skipped: [],
          failed: [],
        },
      });
      await lateJob.promise;
    });

    expect(screen.queryByRole("heading", { name: "Change applied" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Review these accounts again" }),
    ).not.toBeInTheDocument();
  });

  it("previews an exact template revision and recovers safely from stale confirmation", async () => {
    const user = userEvent.setup();
    let snapshotReads = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        snapshotReads += 1;
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [
            { account_id: 41, snapshot: snapshot(41) },
            { account_id: 42, snapshot: snapshot(42, "custom") },
          ],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return previewResponse("apply_entitlement_template") as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs") {
        throw new AdminV2ClientError(
          409,
          "policy_confirmation_stale",
          "The policy preview changed or expired; create a new preview",
        );
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user);
    await user.click(screen.getByRole("radio", { name: "Put on a plan" }));
    await user.type(screen.getByLabelText("Plan key"), "premium");
    await user.type(screen.getByLabelText("Revision"), "7");
    await user.click(screen.getByRole("button", { name: "Preview" }));

    await screen.findByRole("heading", { name: "4. Review and apply" });
    expect(screen.getByText("Left out").parentElement).toHaveTextContent("0");
    const previewCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/platform/accounts/entitlement-bulk/policy-previews");
    expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
      account_ids: [41, 42],
      command: {
        kind: "apply_entitlement_template",
        template_key: "premium",
        template_revision: 7,
        include_custom_profiles: false,
      },
    });

    await user.click(screen.getByRole("checkbox", { name: /checked this change for 2 accounts/ }));
    await user.click(screen.getByRole("button", { name: "Apply to 2 accounts" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Something changed since the preview/,
    );
    await user.click(screen.getByRole("button", { name: "Look up the accounts again" }));
    await waitFor(() => expect(snapshotReads).toBe(2));
    expect(screen.queryByRole("heading", { name: "4. Review and apply" })).not.toBeInTheDocument();
  });

  it("sends the exact derived download patch, re-previews custom-profile opt-in, and shows safe progress", async () => {
    const user = userEvent.setup();
    const previewCommands: unknown[] = [];
    let jobRead = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [
            { account_id: 41, snapshot: snapshot(41) },
            { account_id: 42, snapshot: snapshot(42, "custom") },
          ],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        const body = JSON.parse(String(init?.body));
        previewCommands.push(body.command);
        return previewResponse("derive_entitlement_cohort", {
          custom_profiles_will_remain: body.command.include_custom_profiles ? 0 : 2,
          custom_profiles_will_move: body.command.include_custom_profiles ? 2 : 0,
        }) as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs") {
        return {
          job: {
            job_id: "job-direct-1",
            status: "running",
            progress_current: 1,
            progress_total: 2,
            succeeded: 1,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs/job-direct-1") {
        jobRead += 1;
        return {
          job: {
            job_id: "job-direct-1",
            status: jobRead > 1 ? "completed" : "running",
            progress_current: jobRead > 1 ? 2 : 1,
            progress_total: 2,
            succeeded: jobRead > 1 ? 2 : 1,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user);
    await user.click(screen.getByRole("radio", { name: "Change some settings" }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Start from cohort" }),
      cohorts[0]!.cohort_id,
    );
    await user.type(screen.getByLabelText("Name for the new cohort"), "Download cohort");
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Download original files" }),
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Preview" }));
    expect(
      await screen.findByText("2 with their own settings stay as they are"),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("checkbox", { name: /Also change profiles with their own settings/ }),
    );
    expect(screen.getByText(/the preview is out of date/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update preview" }));
    expect(await screen.findByText("2 with their own settings change too")).toBeInTheDocument();
    expect(previewCommands).toEqual([
      {
        kind: "derive_entitlement_cohort",
        cohort_id: cohorts[0]!.cohort_id,
        name: "Download cohort",
        patch: { download_allowed: true },
        include_custom_profiles: false,
      },
      {
        kind: "derive_entitlement_cohort",
        cohort_id: cohorts[0]!.cohort_id,
        name: "Download cohort",
        patch: { download_allowed: true },
        include_custom_profiles: true,
      },
    ]);

    await user.click(screen.getByRole("checkbox", { name: /checked this change for 2 accounts/ }));
    await user.click(screen.getByRole("button", { name: "Apply to 2 accounts" }));
    expect(await screen.findByRole("heading", { name: "Applying the change" })).toBeInTheDocument();
    expect(screen.getByText("1 of 2 accounts done")).toBeInTheDocument();

    const jobCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/platform/accounts/entitlement-bulk/policy-jobs");
    expect(JSON.parse(String(jobCall?.[1]?.body))).toMatchObject({
      selection_token: "signed-direct-selection",
      confirmation_token: "signed-confirmation",
      command: previewCommands[1],
    });
    expect(JSON.parse(String(jobCall?.[1]?.body)).idempotency_key).toEqual(expect.any(String));
  });

  it("refreshes selectable cohorts after a terminal derived-policy job", async () => {
    const user = userEvent.setup();
    let cohortLoads = 0;
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [{ account_id: 41, snapshot: snapshot(41) }],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) {
        cohortLoads += 1;
        return { cohorts } as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") {
        return previewResponse("derive_entitlement_cohort") as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs") {
        return {
          job: {
            job_id: "job-derived-complete",
            status: "completed",
            progress_current: 1,
            progress_total: 1,
            succeeded: 1,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      if (path === "/platform/accounts/entitlement-bulk/policy-jobs/job-derived-complete") {
        return {
          job: {
            job_id: "job-derived-complete",
            status: "completed",
            progress_current: 1,
            progress_total: 1,
            succeeded: 1,
            skipped: [],
            failed: [],
          },
        } as never;
      }
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user, "41");
    await user.click(screen.getByRole("radio", { name: "Change some settings" }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Start from cohort" }),
      cohorts[0]!.cohort_id,
    );
    await user.type(screen.getByLabelText("Name for the new cohort"), "New selectable cohort");
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Download original files" }),
      "true",
    );
    await user.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByRole("heading", { name: "4. Review and apply" });
    await user.click(screen.getByRole("checkbox", { name: /checked this change for 2 accounts/ }));
    await user.click(screen.getByRole("button", { name: "Apply to 2 accounts" }));

    await screen.findByRole("heading", { name: "Change applied" });
    await waitFor(() => expect(cohortLoads).toBe(2));
  });

  it("restores the managed default without offering a policy-less operation", async () => {
    const user = userEvent.setup();
    const preview = previewResponse("restore_default_entitlement");
    vi.mocked(adminV2Api).mockImplementation(async (path) => {
      if (path === "/platform/accounts/entitlement-snapshots") {
        return {
          observed_at: "2026-08-21T10:15:00Z",
          items: [
            { account_id: 41, snapshot: snapshot(41) },
            { account_id: 42, snapshot: snapshot(42, "legacy_unmanaged") },
          ],
        } as never;
      }
      if (String(path).includes("/entitlement-cohorts")) return { cohorts } as never;
      if (path === "/platform/accounts/entitlement-bulk/policy-previews") return preview as never;
      throw new Error(`Unexpected API route: ${String(path)}`);
    });

    renderPage();
    await reviewAccounts(user);
    expect(screen.queryByText(/remove policy/i)).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Reset to the organization default" }));
    await user.click(screen.getByRole("button", { name: "Preview" }));

    expect(await screen.findByRole("heading", { name: "4. Review and apply" })).toBeInTheDocument();
    const previewCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/platform/accounts/entitlement-bulk/policy-previews");
    expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
      account_ids: [41, 42],
      command: { kind: "restore_default_entitlement", include_custom_profiles: false },
    });
  });
});
