import { expect, test, type Page, type Route } from "@playwright/test";

const organizationID = "10000000-0000-0000-0000-000000000001";
const standardCohortID = "20000000-0000-0000-0000-000000000001";
const premiumCohortID = "20000000-0000-0000-0000-000000000002";
const downloadCohortID = "20000000-0000-0000-0000-000000000003";

type Command = Record<string, unknown> & { kind: string; include_custom_profiles: boolean };

type AccountState = {
  groupID: number;
  state: "managed" | "custom" | "legacy_unmanaged";
  cohortID?: string;
  cohortRevision?: number;
  templateKey?: string;
  templateRevision?: number;
  downloads: boolean;
  customGroupID: number;
  customDownloads: boolean;
};

const accounts: Record<number, AccountState> = {
  41: {
    groupID: 301,
    state: "managed",
    cohortID: standardCohortID,
    cohortRevision: 1,
    templateKey: "standard",
    templateRevision: 4,
    downloads: false,
    customGroupID: 941,
    customDownloads: true,
  },
  42: {
    groupID: 142,
    state: "legacy_unmanaged",
    downloads: false,
    customGroupID: 942,
    customDownloads: true,
  },
};

const cohorts = [cohort(standardCohortID, "Standard", 1, 301, "standard", 4, false)];

test("runs exact-template, derived, custom-profile opt-in, and restore-default jobs", async ({
  page,
}) => {
  const unexpectedRequests: string[] = [];
  const previews = new Map<string, { accountIDs: number[]; command: Command }>();
  const submitted: Array<{ accountIDs: number[]; command: Command }> = [];
  let previewCounter = 0;
  let jobCounter = 0;
  let lastJob = job("none", 0);

  await page.addInitScript(() => {
    localStorage.setItem("refresh_token", "fixture-refresh");
    class FixtureWebSocket {
      static readonly OPEN = 1;
      static readonly CONNECTING = 0;
      readonly OPEN = 1;
      readonly CONNECTING = 0;
      readyState = 1;
      onopen: (() => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onclose: (() => void) | null = null;
      onerror: (() => void) | null = null;
      constructor() {
        queueMicrotask(() => {
          this.onopen?.();
          this.onmessage?.(
            new MessageEvent("message", {
              data: JSON.stringify({ type: "hello", connection_id: "fixture" }),
            }),
          );
        });
      }
      send() {}
      close() {
        this.readyState = 3;
        this.onclose?.();
      }
      addEventListener() {}
      removeEventListener() {}
    }
    Object.defineProperty(window, "WebSocket", { value: FixtureWebSocket, configurable: true });
  });

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/")) return route.continue();
    const path = `${url.pathname}${url.search}`;
    const method = request.method();

    if (method === "GET" && path === "/api/v1/auth/setup")
      return json(route, { needs_setup: false });
    if (method === "GET" && path === "/api/v1/auth/providers") return json(route, []);
    if (method === "POST" && path === "/api/v1/auth/refresh")
      return json(route, {
        access_token: "fixture-account-token",
        refresh_token: "fixture-refresh-next",
        expires_in: 3600,
      });
    if (method === "GET" && path === "/api/v1/auth/me")
      return json(route, {
        id: 7,
        username: "platform-admin",
        email: "admin@example.test",
        role: "admin",
        permissions: [],
        download_allowed: true,
      });
    if (method === "GET" && path === "/api/v1/theme/branding") return json(route, {});
    if (method === "GET" && path === "/api/v1/theme/admin-css")
      return json(route, { vars: "{}", raw_css: "" });
    if (method === "GET" && path === "/api/v1/profiles")
      return json(route, { profiles: [], avatar_upload_enabled: false });
    if (method === "GET" && path === "/api/v1/settings/contract/capabilities")
      return json(route, {
        api_version: 1,
        revision: 5,
        contract_etag: "fixture",
        supports_batched_effective: true,
        supports_idempotent_writes: true,
      });
    if (method === "GET" && path.startsWith("/api/v1/settings/values/effective?"))
      return json(route, { settings: [] });
    if (method === "GET" && path === "/api/v2/organizations")
      return json(route, {
        organizations: [
          {
            id: organizationID,
            slug: "default",
            name: "Default organization",
            default: true,
            membership_id: "30000000-0000-0000-0000-000000000001",
            membership_role: "admin",
            policy_revision: 9,
            security_revision: 4,
          },
        ],
      });
    if (method === "POST" && path === "/api/v2/admin/session")
      return json(route, {
        access_token: "fixture-platform-token",
        expires_at: "2026-08-22T12:00:00Z",
        context: {
          key: "platform",
          scope: "platform",
          name: "Platform",
          status: "active",
          authority: "platform_admin",
          policy_revision: 0,
          security_revision: 0,
        },
      });
    if (method === "GET" && path === "/api/v1/admin/sessions") return json(route, []);
    if (method === "GET" && path === "/api/v1/admin/tasks") return json(route, []);
    if (method === "GET" && path === "/api/v1/libraries") return json(route, []);
    if (method === "GET" && path === "/api/v1/admin/system/build")
      return json(route, {
        display: "fixture",
        revision: "fixture",
        dirty: false,
        vcs_time: "2026-08-21T00:00:00Z",
        available: true,
      });
    if (method === "GET" && path === "/api/v1/policy/capability")
      return json(route, { available: false });
    if (method === "GET" && path === "/api/v1/admin/plugins/installations") return json(route, []);

    if (method === "POST" && path === "/api/v2/admin/platform/accounts/entitlement-snapshots") {
      const body = request.postDataJSON() as { account_ids: number[] };
      return json(route, {
        observed_at: "2026-08-21T10:15:00Z",
        items: body.account_ids.map((accountID) => ({
          account_id: accountID,
          snapshot: accountSnapshot(accountID),
        })),
      });
    }
    if (
      method === "GET" &&
      path ===
        `/api/v2/admin/platform/organizations/${organizationID}/entitlement-cohorts?include_archived=false`
    ) {
      return json(route, { cohorts });
    }
    if (
      method === "POST" &&
      path === "/api/v2/admin/platform/accounts/entitlement-bulk/policy-previews"
    ) {
      const body = request.postDataJSON() as { account_ids: number[]; command: Command };
      previewCounter += 1;
      const selectionToken = `selection-${previewCounter}`;
      previews.set(selectionToken, { accountIDs: body.account_ids, command: body.command });
      return json(route, preview(selectionToken, body.account_ids, body.command), 201);
    }
    if (
      method === "POST" &&
      path === "/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs"
    ) {
      const body = request.postDataJSON() as {
        selection_token: string;
        confirmation_token: string;
        idempotency_key: string;
        command: Command;
      };
      const binding = previews.get(body.selection_token);
      if (!binding || JSON.stringify(binding.command) !== JSON.stringify(body.command)) {
        return json(route, { error: "policy_confirmation_stale", message: "stale" }, 409);
      }
      jobCounter += 1;
      applyCommand(binding.accountIDs, body.command);
      submitted.push(binding);
      lastJob = job(`job-${jobCounter}`, binding.accountIDs.length);
      return json(route, { job: lastJob }, 201);
    }
    if (
      method === "GET" &&
      path === `/api/v2/admin/platform/accounts/entitlement-bulk/policy-jobs/${lastJob.job_id}`
    ) {
      return json(route, { job: lastJob });
    }

    unexpectedRequests.push(`${method} ${path}`);
    return json(route, { error: "unexpected_fixture_route", message: path }, 500);
  });

  await page.goto("/admin/platform/direct-accounts/bulk");
  await expect(page.getByRole("heading", { name: "Bulk direct-account policies" })).toBeVisible();

  await review(page, "41");
  const account41 = page.getByRole("article", { name: "Account 41 authoritative policy" });
  await expect(account41).toContainText("Template standard · revision 4");
  await expect(account41).toContainText("Custom exception");
  await expect(account41).toContainText("Downloads Not allowed");

  await page.getByRole("radio", { name: "Apply an exact template revision" }).check();
  await page.getByLabel("Template key").fill("premium");
  await page.getByRole("spinbutton", { name: "Template revision" }).fill("7");
  await submitPreviewedJob(page);
  await expect(page.getByRole("heading", { name: "Policy job completed" })).toBeVisible();

  await page.getByRole("button", { name: "Refresh authoritative selection" }).click();
  await expect(account41).toContainText("Template premium · revision 7");
  await expect(account41).toContainText("Custom exception");

  await review(page, "42");
  await page.getByRole("radio", { name: "Derive a policy for this selection" }).check();
  await page.getByRole("combobox", { name: "Base cohort" }).selectOption(standardCohortID);
  await page.getByLabel("Derived cohort name").fill("Downloads enabled");
  await page.getByRole("combobox", { name: "Downloads", exact: true }).selectOption("true");
  await page.getByRole("button", { name: "Preview policy impact" }).click();
  await expect(page.getByText("1 custom profiles remain unchanged")).toBeVisible();
  await confirmAndStart(page);
  await page.getByRole("button", { name: "Refresh authoritative selection" }).click();
  const account42 = page.getByRole("article", { name: "Account 42 authoritative policy" });
  await expect(account42).toContainText("Downloads Allowed");
  await expect(account42).toContainText("Custom exception");

  await review(page, "41, 42");
  await page.getByRole("radio", { name: "Move to an existing cohort" }).check();
  await page.getByRole("combobox", { name: "Target cohort" }).selectOption(downloadCohortID);
  await page.getByRole("button", { name: "Preview policy impact" }).click();
  await page.getByRole("checkbox", { name: "Move custom profiles too" }).click();
  await page.getByRole("button", { name: "Recalculate policy impact" }).click();
  await expect(page.getByText("2 custom profiles move")).toBeVisible();
  await confirmAndStart(page);

  await page.getByRole("button", { name: "Refresh authoritative selection" }).click();
  await page.getByRole("radio", { name: "Restore the managed default" }).check();
  await expect(page.getByText(/Every selected account remains attached/)).toBeVisible();
  await submitPreviewedJob(page);
  await page.getByRole("button", { name: "Refresh authoritative selection" }).click();
  await expect(
    page.getByRole("article", { name: "Account 41 authoritative policy" }),
  ).toContainText("managed");
  await expect(
    page.getByRole("article", { name: "Account 42 authoritative policy" }),
  ).toContainText("managed");

  expect(cohorts.map((item) => item.name)).toEqual(["Standard", "Premium", "Downloads enabled"]);
  expect(submitted.map((item) => item.accountIDs)).toEqual([[41], [42], [41, 42], [41, 42]]);
  expect(submitted[1]?.command).toMatchObject({
    kind: "derive_entitlement_cohort",
    patch: { download_allowed: true },
    include_custom_profiles: false,
  });
  expect(submitted[2]?.command).toMatchObject({ include_custom_profiles: true });
  expect(submitted[3]?.command).toEqual({
    kind: "restore_default_entitlement",
    include_custom_profiles: false,
  });
  expect(unexpectedRequests).toEqual([]);

  function applyCommand(accountIDs: number[], command: Command) {
    if (command.kind === "apply_entitlement_template") {
      if (!cohorts.some((item) => item.cohort_id === premiumCohortID))
        cohorts.push(cohort(premiumCohortID, "Premium", 1, 302, "premium", 7, false));
      for (const accountID of accountIDs)
        Object.assign(accounts[accountID]!, {
          groupID: 302,
          state: "managed",
          cohortID: premiumCohortID,
          cohortRevision: 1,
          templateKey: "premium",
          templateRevision: 7,
        });
    } else if (command.kind === "derive_entitlement_cohort") {
      if (!cohorts.some((item) => item.cohort_id === downloadCohortID))
        cohorts.push(cohort(downloadCohortID, String(command.name), 1, 303, "standard", 4, true));
      for (const accountID of accountIDs)
        Object.assign(accounts[accountID]!, {
          groupID: 303,
          state: "managed",
          cohortID: downloadCohortID,
          cohortRevision: 1,
          templateKey: "standard",
          templateRevision: 4,
          downloads: true,
        });
    } else if (command.kind === "assign_entitlement_cohort") {
      for (const accountID of accountIDs) {
        Object.assign(accounts[accountID]!, {
          groupID: 303,
          state: "managed",
          cohortID: downloadCohortID,
          cohortRevision: 1,
          templateKey: "standard",
          templateRevision: 4,
          downloads: true,
        });
        if (command.include_custom_profiles) accounts[accountID]!.customGroupID = 303;
      }
    } else if (command.kind === "restore_default_entitlement") {
      for (const accountID of accountIDs)
        Object.assign(accounts[accountID]!, {
          groupID: 300,
          customGroupID:
            accounts[accountID]!.customGroupID === accounts[accountID]!.groupID
              ? 300
              : accounts[accountID]!.customGroupID,
          state: "managed",
          cohortID: "20000000-0000-0000-0000-000000000000",
          cohortRevision: 1,
          templateKey: undefined,
          templateRevision: undefined,
          downloads: false,
        });
    }
  }
});

async function review(page: Page, accountIDs: string) {
  await page.getByLabel("Server account IDs").fill(accountIDs);
  await page.getByRole("button", { name: "Review selected accounts" }).click();
  const first = accountIDs.split(/[\s,]+/)[0];
  await expect(page.getByRole("heading", { name: `Account ${first}` })).toBeVisible();
}

async function submitPreviewedJob(page: Page) {
  await page.getByRole("button", { name: "Preview policy impact" }).click();
  await expect(page.getByRole("heading", { name: "Review authoritative impact" })).toBeVisible();
  await confirmAndStart(page);
}

async function confirmAndStart(page: Page) {
  await page
    .getByRole("checkbox", { name: "I confirm this exact account set and policy target" })
    .check();
  await page.getByRole("button", { name: "Start policy job" }).click();
  await expect(page.getByRole("heading", { name: "Policy job completed" })).toBeVisible();
}

function accountSnapshot(accountID: number) {
  const current = accounts[accountID];
  if (!current) throw new Error(`Unknown fixture account ${accountID}`);
  return {
    observed_at: "2026-08-21T10:15:00Z",
    organization_id: organizationID,
    account_id: accountID,
    group_id: current.groupID,
    cohort_id: current.cohortID,
    cohort_revision: current.cohortRevision,
    source_template_key: current.templateKey,
    source_template_revision: current.templateRevision,
    state: current.state,
    policy_revision: 8 + accountID,
    policy: effectivePolicy(current.downloads),
    profiles: [
      {
        profile_id: `profile-${accountID}-inherited`,
        profile_name: `Inherited ${accountID}`,
        group_id: current.groupID,
        inherits_account: true,
        state: current.state,
        policy: effectivePolicy(current.downloads),
      },
      {
        profile_id: `profile-${accountID}-custom`,
        profile_name: `Custom ${accountID}`,
        group_id: current.customGroupID,
        inherits_account: current.customGroupID === current.groupID,
        state: current.customGroupID === current.groupID ? "managed" : "custom",
        policy: effectivePolicy(
          current.customGroupID === current.groupID ? current.downloads : current.customDownloads,
        ),
      },
    ],
  };
}

function effectivePolicy(downloads: boolean) {
  return {
    library_ids: [12, 18],
    playback_allowed: true,
    max_streams: 2,
    max_profiles: 4,
    transcode_allowed: true,
    audio_transcode_allowed: true,
    max_transcodes: 1,
    download_allowed: downloads,
    download_transcode_allowed: false,
    max_playback_quality: "1080p",
    allowed_permissions: ["request_media"],
    requests_allowed: true,
  };
}

function cohort(
  cohortID: string,
  name: string,
  revision: number,
  groupID: number,
  templateKey: string,
  templateRevision: number,
  downloads: boolean,
) {
  return {
    cohort_id: cohortID,
    organization_id: organizationID,
    name,
    revision,
    access_group_id: groupID,
    source_template_key: templateKey,
    source_template_revision: templateRevision,
    derivation_kind: downloads ? "policy_patch" : "exact_template",
    policy: effectivePolicy(downloads),
    policy_digest: `digest-${cohortID}`,
    member_count: 0,
    archived: false,
    created_at: "2026-08-21T10:00:00Z",
  };
}

function preview(selectionToken: string, accountIDs: number[], command: Command) {
  const includeCustomProfiles = command.include_custom_profiles;
  const targetDownloads =
    command.kind === "derive_entitlement_cohort" ||
    (command.kind === "assign_entitlement_cohort" && command.cohort_id === downloadCohortID);
  return {
    selection: {
      token: selectionToken,
      matched: accountIDs.length,
      excluded: 0,
      expires_at: "2026-08-21T11:15:00Z",
    },
    preview: {
      matched: accountIDs.length,
      excluded: 0,
      already_compliant: 0,
      inherited_profiles_will_move: accountIDs.length,
      custom_profiles_will_remain: includeCustomProfiles ? 0 : accountIDs.length,
      custom_profiles_will_move: includeCustomProfiles ? accountIDs.length : 0,
      ineligible_or_stale: 0,
      current_cohorts: [],
      target: {
        kind: command.kind,
        cohort_id:
          command.kind === "derive_entitlement_cohort"
            ? downloadCohortID
            : (command.cohort_id as string | undefined),
        cohort_revision: 1,
        group_id: command.kind === "restore_default_entitlement" ? 300 : 303,
        template_key: command.template_key as string | undefined,
        template_revision: command.template_revision as number | undefined,
        name: command.name as string | undefined,
        policy_digest: `target-${selectionToken}`,
        policy: effectivePolicy(Boolean(targetDownloads)),
      },
      diff: [{ field: "download_allowed", changed_accounts: accountIDs.length }],
      selection_expires_at: "2026-08-21T11:15:00Z",
      confirmation_expires_at: "2026-08-21T10:45:00Z",
      confirmation_token: `confirmation-${selectionToken}`,
    },
  };
}

function job(jobID: string, total: number) {
  return {
    job_id: jobID,
    status: "completed",
    progress_current: total,
    progress_total: total,
    succeeded: total,
    skipped: [],
    failed: [],
  };
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
