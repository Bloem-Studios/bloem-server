// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import DirectAccountPolicyBulkPage from "./DirectAccountPolicyBulkPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

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
  await user.type(screen.getByLabelText("Server account IDs"), ids);
  await user.click(screen.getByRole("button", { name: "Review selected accounts" }));
  await screen.findByRole("heading", { name: "Account 41" });
}

describe("DirectAccountPolicyBulkPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("requires a unique explicit bounded Server account selection", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(screen.queryByRole("radiogroup", { name: "Policy operation" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Server account IDs"), "41, 41");
    await user.click(screen.getByRole("button", { name: "Review selected accounts" }));

    expect(screen.getByRole("alert")).toHaveTextContent(/duplicate Server account IDs/i);
    expect(adminV2Api).not.toHaveBeenCalled();
  });

  it("renders complete authoritative account and profile policy before enabling operations", async () => {
    const user = userEvent.setup();
    installBaseRoutes();
    renderPage();
    await reviewAccounts(user);

    const account41 = screen.getByRole("article", { name: "Account 41 authoritative policy" });
    expect(account41).toHaveTextContent("managed");
    expect(account41).toHaveTextContent("Template premium · revision 7");
    expect(account41).toHaveTextContent("Cohort revision 3");
    expect(account41).toHaveTextContent("Policy revision 8");
    expect(account41).toHaveTextContent("Libraries 12, 18");
    expect(account41).toHaveTextContent("Audio transcoding Allowed");
    expect(account41).toHaveTextContent("Custom 41");
    expect(account41).toHaveTextContent("Custom exception");
    expect(account41).toHaveTextContent("legacy unmanaged");
    expect(screen.getByText(/Observed together at/)).toHaveTextContent("2026");

    expect(screen.getByRole("radiogroup", { name: "Policy operation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview policy impact" })).toBeDisabled();
    expect(screen.getAllByRole("radio").every((radio) => !radio.hasAttribute("checked"))).toBe(
      true,
    );
    expect(screen.queryByRole("combobox", { name: "Target cohort" })).not.toBeInTheDocument();
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

    expect(screen.getByRole("alert")).toHaveTextContent(/Account 999.*not found/i);
    expect(screen.queryByRole("radiogroup", { name: "Policy operation" })).not.toBeInTheDocument();
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
    await user.click(screen.getByRole("radio", { name: "Restore the managed default" }));
    await user.click(screen.getByRole("button", { name: "Preview policy impact" }));
    await screen.findByRole("heading", { name: "Review authoritative impact" });
    await user.click(screen.getByRole("checkbox", { name: /I confirm this exact account set/i }));

    await user.clear(screen.getByLabelText("Server account IDs"));
    await user.type(screen.getByLabelText("Server account IDs"), "99");

    expect(
      screen.queryByRole("heading", { name: "Review authoritative impact" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start policy job" })).not.toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Policy operation" })).not.toBeInTheDocument();
  });

  it("does not retain an old reviewed workflow when replacement account IDs are invalid", async () => {
    const user = userEvent.setup();
    installBaseRoutes();
    renderPage();
    await reviewAccounts(user);

    await user.clear(screen.getByLabelText("Server account IDs"));
    await user.type(screen.getByLabelText("Server account IDs"), "41, invalid");
    await user.click(screen.getByRole("button", { name: "Review selected accounts" }));

    expect(screen.getByRole("alert")).toHaveTextContent(/positive whole numbers/i);
    expect(
      screen.queryByRole("heading", { name: "Authoritative observations" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "Policy operation" })).not.toBeInTheDocument();
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
    await user.click(screen.getByRole("radio", { name: "Apply an exact template revision" }));
    await user.type(screen.getByLabelText("Template key"), "premium");
    await user.type(screen.getByLabelText("Template revision"), "7");
    await user.click(screen.getByRole("button", { name: "Preview policy impact" }));

    await screen.findByRole("heading", { name: "Review authoritative impact" });
    expect(screen.getByText("Accounts 41, 42")).toBeInTheDocument();
    expect(screen.getByText("Excluded").parentElement).toHaveTextContent("0");
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

    await user.click(screen.getByRole("checkbox", { name: /I confirm this exact account set/i }));
    await user.click(screen.getByRole("button", { name: "Start policy job" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/preview changed or expired/i);
    await user.click(screen.getByRole("button", { name: "Refresh authoritative selection" }));
    await waitFor(() => expect(snapshotReads).toBe(2));
    expect(
      screen.queryByRole("heading", { name: "Review authoritative impact" }),
    ).not.toBeInTheDocument();
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
    await user.click(screen.getByRole("radio", { name: "Derive a policy for this selection" }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Base cohort" }),
      cohorts[0]!.cohort_id,
    );
    await user.type(screen.getByLabelText("Derived cohort name"), "Download cohort");
    await user.selectOptions(screen.getByRole("combobox", { name: "Downloads" }), "true");
    await user.click(screen.getByRole("button", { name: "Preview policy impact" }));
    expect(await screen.findByText("2 custom profiles remain unchanged")).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: "Move custom profiles too" }));
    expect(screen.getByText(/Preview is out of date/i)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Recalculate policy impact" }));
    expect(await screen.findByText("2 custom profiles move")).toBeInTheDocument();
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

    await user.click(screen.getByRole("checkbox", { name: /I confirm this exact account set/i }));
    await user.click(screen.getByRole("button", { name: "Start policy job" }));
    expect(await screen.findByRole("heading", { name: "Policy job running" })).toBeInTheDocument();
    expect(screen.getByText("1 of 2 processed")).toBeInTheDocument();

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
    await user.click(screen.getByRole("radio", { name: "Derive a policy for this selection" }));
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Base cohort" }),
      cohorts[0]!.cohort_id,
    );
    await user.type(screen.getByLabelText("Derived cohort name"), "New selectable cohort");
    await user.selectOptions(screen.getByRole("combobox", { name: "Downloads" }), "true");
    await user.click(screen.getByRole("button", { name: "Preview policy impact" }));
    await screen.findByRole("heading", { name: "Review authoritative impact" });
    await user.click(screen.getByRole("checkbox", { name: /I confirm this exact account set/i }));
    await user.click(screen.getByRole("button", { name: "Start policy job" }));

    await screen.findByRole("heading", { name: "Policy job completed" });
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
    await user.click(screen.getByRole("radio", { name: "Restore the managed default" }));
    await user.click(screen.getByRole("button", { name: "Preview policy impact" }));

    expect(
      await screen.findByText(
        /Every selected account remains attached to an enforceable managed policy/i,
      ),
    ).toBeInTheDocument();
    const previewCall = vi
      .mocked(adminV2Api)
      .mock.calls.find(([path]) => path === "/platform/accounts/entitlement-bulk/policy-previews");
    expect(JSON.parse(String(previewCall?.[1]?.body))).toEqual({
      account_ids: [41, 42],
      command: { kind: "restore_default_entitlement", include_custom_profiles: false },
    });
  });
});
