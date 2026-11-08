import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";

import { adminV2Api } from "@/api/adminV2Client";
import { AccountPicker } from "@/components/admin/policy/AccountPicker";
import { PolicyChangeForm } from "@/components/admin/policy/PolicyChangeForm";
import {
  buildPolicyCommand,
  emptyPolicyChangeDraft,
  type PlanChoice,
  type PolicyChangeDraft,
} from "@/components/admin/policy/policyDraft";
import { useAccountNames } from "@/components/admin/policy/useAccountNames";
import {
  ConfirmChange,
  CustomProfilesOption,
  PolicyJobView,
  PolicySummary,
  PolicyTarget,
  PreviewImpact,
  StepList,
} from "@/components/admin/policy/PolicyReview";
import {
  formatDateTime,
  needsFreshSelection,
  policyErrorMessage,
  policyStateLabel,
  type NamedLibrary,
} from "@/components/admin/policy/policyVocabulary";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import { useEntitlementTemplates } from "@/hooks/queries/admin/entitlementTemplates";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import type {
  EntitlementCohort,
  PolicyCommand,
  PolicyPreview,
} from "@/hooks/queries/admin/entitlementCohorts";
import type { PeopleBulkJob, PeopleSelection } from "@/hooks/queries/admin/organizationPeople";

type AccountPolicyState = "managed" | "custom" | "legacy_unmanaged";

interface AuthoritativePolicy {
  library_ids: number[] | null;
  playback_allowed: boolean;
  max_streams: number;
  max_profiles: number;
  transcode_allowed: boolean;
  audio_transcode_allowed: boolean;
  max_transcodes: number;
  download_allowed: boolean;
  download_transcode_allowed: boolean;
  max_playback_quality: string;
  allowed_permissions: string[] | null;
  requests_allowed: boolean;
}

interface ProfilePolicySnapshot {
  profile_id: string;
  profile_name: string;
  group_id: number;
  inherits_account: boolean;
  state: AccountPolicyState;
  policy: AuthoritativePolicy;
}

interface AccountPolicySnapshot {
  observed_at: string;
  organization_id: string;
  account_id: number;
  group_id: number;
  cohort_id?: string;
  cohort_revision?: number;
  source_template_key?: string;
  source_template_revision?: number;
  state: AccountPolicyState;
  policy_revision: number;
  policy: AuthoritativePolicy;
  profiles: ProfilePolicySnapshot[];
}

interface SnapshotResult {
  account_id: number;
  snapshot?: AccountPolicySnapshot;
  error?: "not_found" | "stale" | string;
}

interface SnapshotResponse {
  observed_at: string;
  items: SnapshotResult[];
}

interface PreviewResponse {
  selection: PeopleSelection;
  preview: PolicyPreview;
}

interface PreviewBinding extends PreviewResponse {
  command: PolicyCommand;
  accountIDs: number[];
  generation: number;
}

interface SelectionRequest {
  accountIDs: number[];
  generation: number;
}

interface PreviewRequest extends SelectionRequest {
  command: PolicyCommand;
}

interface JobRequest extends SelectionRequest {
  binding: PreviewBinding;
}

interface CancelJobRequest extends SelectionRequest {
  jobID: string;
}

const STEPS = ["Accounts", "Current settings", "Change", "Review and apply"];

export default function DirectAccountPolicyBulkPage() {
  useDocumentTitle("Change many accounts");
  const [pickedIDs, setPickedIDs] = useState<number[]>([]);
  const [pastedIDs, setPastedIDs] = useState("");
  const [selectedIDs, setSelectedIDs] = useState<number[]>([]);
  const [snapshotData, setSnapshotData] = useState<SnapshotResponse>();
  const [selectionError, setSelectionError] = useState("");
  const [draft, setDraft] = useState<PolicyChangeDraft>(() => emptyPolicyChangeDraft());
  const [includeCustomProfiles, setIncludeCustomProfiles] = useState(false);
  const [previewBinding, setPreviewBinding] = useState<PreviewBinding>();
  const [previewOutdated, setPreviewOutdated] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob>();
  const [validationError, setValidationError] = useState("");
  const submitGuard = useRef(false);
  const idempotencyKey = useRef("");
  const refreshedTerminalJob = useRef<string | undefined>(undefined);
  const requestGeneration = useRef(0);
  const selectedIDsRef = useRef<number[]>([]);
  const queryClient = useQueryClient();
  const accountName = useAccountNames();
  const libraryQuery = useAdminLibraries();
  const libraries = useMemo<NamedLibrary[]>(
    () => (libraryQuery.data ?? []).map(({ id, name }) => ({ id, name })),
    [libraryQuery.data],
  );
  const templates = useEntitlementTemplates(false);
  const plans = useMemo<PlanChoice[] | undefined>(
    () =>
      templates.data
        ?.filter((template) => template.enabled && !template.archived)
        .map(({ key, name, revision }) => ({ key, name, revision })),
    [templates.data],
  );

  const snapshots = useMutation({
    mutationFn: ({ accountIDs }: SelectionRequest) =>
      adminV2Api<SnapshotResponse>("/platform/accounts/entitlement-snapshots", {
        method: "POST",
        body: JSON.stringify({ account_ids: accountIDs }),
      }),
    onSuccess: (response, request) => {
      if (!isCurrentSelection(request)) return;
      if (!snapshotMatchesSelection(response, request.accountIDs)) {
        discardReviewedSelection();
        setSelectionError(
          "The server answered for different accounts. Look up the accounts again.",
        );
        return;
      }
      setSnapshotData(response);
    },
  });

  const validSnapshots = snapshotData?.items.flatMap((item) =>
    item.snapshot ? [item.snapshot] : [],
  );
  const incompleteResults = snapshotData?.items.filter((item) => !item.snapshot) ?? [];
  const oneOrganization =
    validSnapshots &&
    validSnapshots.length === selectedIDs.length &&
    new Set(validSnapshots.map((item) => item.organization_id)).size === 1;
  const organizationID = oneOrganization ? validSnapshots[0]?.organization_id : undefined;

  const cohorts = useQuery({
    queryKey: ["admin-v2", "platform", "direct-account-cohorts", organizationID],
    queryFn: () =>
      adminV2Api<{ cohorts: EntitlementCohort[] }>(
        `/platform/organizations/${encodeURIComponent(organizationID ?? "")}/entitlement-cohorts?include_archived=false`,
      ).then((result) => result.cohorts ?? []),
    enabled: Boolean(organizationID),
  });

  const createPreview = useMutation({
    mutationFn: ({ accountIDs, command }: PreviewRequest) =>
      adminV2Api<PreviewResponse>("/platform/accounts/entitlement-bulk/policy-previews", {
        method: "POST",
        body: JSON.stringify({ account_ids: accountIDs, command }),
      }),
  });

  const createJob = useMutation({
    mutationFn: ({ binding: { selection, preview, command } }: JobRequest) =>
      adminV2Api<{ job: PeopleBulkJob }>("/platform/accounts/entitlement-bulk/policy-jobs", {
        method: "POST",
        body: JSON.stringify({
          selection_token: selection.token,
          confirmation_token: preview.confirmation_token,
          idempotency_key: idempotencyKey.current,
          command,
        }),
      }).then((result) => result.job),
    onSuccess: (job, request) => {
      if (isCurrentSelection(request)) setSubmittedJob(job);
    },
  });

  const polledJob = useQuery({
    queryKey: ["admin-v2", "platform", "direct-account-policy-job", submittedJob?.job_id],
    queryFn: () =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/platform/accounts/entitlement-bulk/policy-jobs/${encodeURIComponent(submittedJob?.job_id ?? "")}`,
      ).then((result) => result.job),
    enabled: Boolean(submittedJob?.job_id),
    refetchInterval: (query) => {
      const status = (query.state.data as PeopleBulkJob | undefined)?.status;
      return status === "queued" || status === "running" || !status ? 1000 : false;
    },
  });
  const visibleJob = polledJob.data ?? submittedJob;

  useEffect(() => {
    if (
      organizationID &&
      visibleJob &&
      ["completed", "failed", "cancelled"].includes(visibleJob.status) &&
      refreshedTerminalJob.current !== visibleJob.job_id
    ) {
      refreshedTerminalJob.current = visibleJob.job_id;
      void queryClient.invalidateQueries({
        queryKey: ["admin-v2", "platform", "direct-account-cohorts", organizationID],
      });
    }
  }, [organizationID, queryClient, visibleJob]);

  const cancelJob = useMutation({
    mutationFn: ({ jobID }: CancelJobRequest) =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/platform/accounts/entitlement-bulk/policy-jobs/${encodeURIComponent(jobID)}/cancel`,
        { method: "POST", body: "{}" },
      ).then((result) => result.job),
    onSuccess: (job, request) => {
      if (isCurrentSelection(request)) setSubmittedJob(job);
    },
  });

  function resetPolicyWorkflow() {
    setDraft(emptyPolicyChangeDraft());
    setIncludeCustomProfiles(false);
    setPreviewBinding(undefined);
    setPreviewOutdated(false);
    setConfirmed(false);
    setSubmittedJob(undefined);
    setValidationError("");
    createPreview.reset();
    createJob.reset();
    idempotencyKey.current = "";
    refreshedTerminalJob.current = undefined;
  }

  function discardReviewedSelection() {
    requestGeneration.current += 1;
    selectedIDsRef.current = [];
    setSelectedIDs([]);
    setSnapshotData(undefined);
    snapshots.reset();
    resetPolicyWorkflow();
  }

  function accountsChanged() {
    setSelectionError("");
    discardReviewedSelection();
  }

  async function reviewSelection() {
    discardReviewedSelection();
    const parsed = combineAccountIDs(pickedIDs, pastedIDs);
    if (parsed instanceof Error) {
      setSelectionError(parsed.message);
      return;
    }
    setSelectionError("");
    selectedIDsRef.current = parsed;
    setSelectedIDs(parsed);
    const request = { accountIDs: [...parsed], generation: requestGeneration.current };
    try {
      await snapshots.mutateAsync(request);
    } catch {
      // The mutation's bounded API error remains visible below the form.
    }
  }

  function invalidatePreview() {
    requestGeneration.current += 1;
    if (previewBinding) setPreviewOutdated(true);
    setPreviewBinding(undefined);
    setConfirmed(false);
    setValidationError("");
    createPreview.reset();
    createJob.reset();
    idempotencyKey.current = "";
  }

  async function previewImpact() {
    const command = buildPolicyCommand(draft, cohorts.data ?? [], includeCustomProfiles);
    if (command instanceof Error) {
      setValidationError(command.message);
      return;
    }
    setValidationError("");
    const request: PreviewRequest = {
      accountIDs: [...selectedIDsRef.current],
      command,
      generation: requestGeneration.current,
    };
    try {
      const response = await createPreview.mutateAsync(request);
      if (!isCurrentSelection(request)) return;
      setPreviewBinding({ ...response, ...request });
      setPreviewOutdated(false);
    } catch {
      // The mutation exposes the safe API error.
    }
  }

  async function submitJob() {
    if (submitGuard.current || !confirmed || !previewBinding || !isCurrentSelection(previewBinding))
      return;
    submitGuard.current = true;
    if (!idempotencyKey.current) idempotencyKey.current = newIdempotencyKey();
    refreshedTerminalJob.current = undefined;
    try {
      await createJob.mutateAsync({
        binding: previewBinding,
        accountIDs: [...previewBinding.accountIDs],
        generation: previewBinding.generation,
      });
    } catch {
      // The mutation exposes the safe API error and retains the reviewed preview.
    } finally {
      submitGuard.current = false;
    }
  }

  function isCurrentSelection(request: SelectionRequest): boolean {
    return (
      request.generation === requestGeneration.current &&
      sameAccountIDs(request.accountIDs, selectedIDsRef.current)
    );
  }

  const step = previewBinding ? 4 : organizationID ? 3 : snapshotData ? 2 : 1;
  const accountCount = pickedIDs.length + countPasted(pastedIDs);

  return (
    <section className="admin-page space-y-6">
      <header className="page-header">
        <div className="space-y-2">
          <p className="text-muted-foreground text-sm">
            <Link
              className="hover:text-foreground underline-offset-4 transition-colors hover:underline"
              to="/admin/platform/direct-accounts"
            >
              Direct accounts
            </Link>{" "}
            / Change many accounts
          </p>
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Change many accounts
          </h1>
          <p className="page-subtitle max-w-3xl">
            Put up to 10,000 direct accounts on a plan, or change their settings, in one go. You see
            exactly what changes before anything is applied.
          </p>
        </div>
      </header>

      {!visibleJob ? <StepList steps={STEPS} current={step} /> : null}

      <section
        className="surface-panel space-y-4 rounded-2xl p-5"
        aria-labelledby="direct-account-selection"
      >
        <h2 id="direct-account-selection" className="text-lg font-medium">
          1. Choose accounts
        </h2>
        <AccountPicker
          id="direct-account-search"
          label="Find accounts"
          selected={pickedIDs}
          onChange={(ids) => {
            setPickedIDs(ids);
            accountsChanged();
          }}
        />
        <details className="text-sm" open={Boolean(pastedIDs)}>
          <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
            Paste a list of account numbers
          </summary>
          <div className="mt-3 space-y-1.5">
            <Label htmlFor="direct-account-ids">Account numbers</Label>
            <textarea
              id="direct-account-ids"
              className="border-input bg-background min-h-24 w-full rounded-md border px-3 py-2 text-sm"
              placeholder="41, 42, 57"
              value={pastedIDs}
              onChange={(event) => {
                setPastedIDs(event.target.value);
                accountsChanged();
              }}
            />
            <p className="text-muted-foreground text-xs">
              Separate them with commas, spaces or new lines.
            </p>
          </div>
        </details>
        {selectionError ? (
          <p className="text-destructive text-sm" role="alert">
            {selectionError}
          </p>
        ) : null}
        {snapshots.error ? <SafeError error={snapshots.error} /> : null}
        <Button
          type="button"
          disabled={snapshots.isPending || accountCount === 0}
          onClick={() => void reviewSelection()}
        >
          {snapshots.isPending ? "Looking up accounts…" : "Look up current settings"}
        </Button>
      </section>

      {snapshotData ? (
        <section className="space-y-4" aria-labelledby="current-settings">
          <div>
            <h2 id="current-settings" className="text-lg font-medium">
              2. Current settings
            </h2>
            <p className="text-muted-foreground mt-1 text-sm">
              As of {formatDateTime(snapshotData.observed_at)}
            </p>
          </div>
          {incompleteResults.length ? (
            <div
              className="border-destructive/30 bg-destructive/10 text-destructive space-y-1 rounded-xl border p-4 text-sm"
              role="alert"
            >
              {incompleteResults.map((item) => (
                <p key={item.account_id}>
                  {accountName(item.account_id)}{" "}
                  {item.error === "not_found"
                    ? "isn't a direct account on this server."
                    : item.error === "stale"
                      ? "changed while it was being read."
                      : "couldn't be read."}
                </p>
              ))}
              <p>Remove these accounts or try again before you continue.</p>
            </div>
          ) : !oneOrganization ? (
            <div
              className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4 text-sm"
              role="alert"
            >
              These accounts belong to different places. Choose direct accounts only, then look them
              up again.
            </div>
          ) : null}
          <div className="grid gap-4 xl:grid-cols-2">
            {snapshotData.items.map((item) =>
              item.snapshot ? (
                <AccountPolicyCard
                  key={item.account_id}
                  snapshot={item.snapshot}
                  name={accountName(item.account_id)}
                  libraries={libraries}
                />
              ) : null,
            )}
          </div>
        </section>
      ) : null}

      {organizationID ? (
        <section
          className="surface-panel space-y-5 rounded-2xl p-5"
          aria-labelledby="policy-operation-heading"
        >
          <h2 id="policy-operation-heading" className="text-lg font-medium">
            3. What should change?
          </h2>
          <PolicyChangeForm
            draft={draft}
            onChange={(next) => {
              setDraft(next);
              invalidatePreview();
            }}
            cohorts={cohorts.data ?? []}
            plans={plans}
            libraries={libraries}
            error={validationError}
            radioName="direct-policy"
          />
          {draft.operation ? (
            <CustomProfilesOption
              checked={includeCustomProfiles}
              onCheckedChange={(checked) => {
                setIncludeCustomProfiles(checked);
                invalidatePreview();
              }}
            />
          ) : null}
          {cohorts.error ? <SafeError error={cohorts.error} /> : null}
          {createPreview.error ? <SafeError error={createPreview.error} /> : null}
          <Button
            type="button"
            disabled={!draft.operation || createPreview.isPending}
            onClick={() => void previewImpact()}
          >
            {createPreview.isPending ? "Checking…" : previewOutdated ? "Update preview" : "Preview"}
          </Button>
        </section>
      ) : null}

      {previewOutdated && !previewBinding ? (
        <p className="border-border bg-muted/40 rounded-xl border p-4 text-sm" role="status">
          You changed an option, so the preview is out of date. Update it before you apply.
        </p>
      ) : null}

      {previewBinding && !visibleJob ? (
        <section
          className="surface-panel space-y-5 rounded-2xl p-5"
          aria-labelledby="review-heading"
        >
          <h2 id="review-heading" className="text-lg font-medium">
            4. Review and apply
          </h2>
          <PreviewImpact preview={previewBinding.preview} />
          <PolicyTarget target={previewBinding.preview.target} libraries={libraries} />
          <ConfirmChange
            accounts={previewBinding.selection.matched}
            expiresAt={formatDateTime(previewBinding.preview.confirmation_expires_at)}
            checked={confirmed}
            onCheckedChange={setConfirmed}
          />
          {createJob.error ? <SafeError error={createJob.error} /> : null}
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              disabled={!confirmed || createJob.isPending}
              onClick={() => void submitJob()}
            >
              Apply to {previewBinding.selection.matched.toLocaleString()}{" "}
              {previewBinding.selection.matched === 1 ? "account" : "accounts"}
            </Button>
            {needsFreshSelection(createJob.error) ? (
              <Button type="button" variant="outline" onClick={() => void reviewSelection()}>
                Look up the accounts again
              </Button>
            ) : null}
          </div>
        </section>
      ) : null}

      {visibleJob ? (
        <section className="surface-panel rounded-2xl p-5">
          <PolicyJobView
            job={visibleJob}
            error={polledJob.error ?? cancelJob.error}
            cancelling={cancelJob.isPending}
            accountName={accountName}
            onCancel={() =>
              void cancelJob
                .mutateAsync({
                  jobID: visibleJob.job_id,
                  accountIDs: [...selectedIDsRef.current],
                  generation: requestGeneration.current,
                })
                .catch(() => undefined)
            }
            onStartOver={() => void reviewSelection()}
          />
        </section>
      ) : null}
    </section>
  );
}

function AccountPolicyCard({
  snapshot,
  name,
  libraries,
}: {
  snapshot: AccountPolicySnapshot;
  name: string;
  libraries: NamedLibrary[];
}) {
  const source = snapshot.source_template_key
    ? `Plan ${snapshot.source_template_key}, revision ${snapshot.source_template_revision}`
    : policyStateLabel(snapshot.state);
  return (
    <article
      className="surface-panel space-y-4 rounded-2xl p-5"
      aria-label={`${name} current settings`}
    >
      <div>
        <h3 className="text-base font-medium">{name}</h3>
        <p className="text-muted-foreground text-sm">{source}</p>
      </div>
      <PolicySummary policy={snapshot.policy} libraries={libraries} label={`${name} settings`} />
      {snapshot.profiles.length ? (
        <details className="text-sm">
          <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
            Profiles ({snapshot.profiles.length})
          </summary>
          <ul className="mt-3 space-y-3">
            {snapshot.profiles.map((profile) => (
              <li
                key={profile.profile_id}
                className="border-border space-y-2 rounded-lg border p-3"
              >
                <p>
                  <span className="font-medium">{profile.profile_name}</span>
                  <span className="text-muted-foreground">
                    {" "}
                    · {profile.inherits_account ? "Follows the account" : "Has its own settings"}
                  </span>
                </p>
                {!profile.inherits_account ? (
                  <PolicySummary
                    policy={profile.policy}
                    libraries={libraries}
                    label={`${profile.profile_name} settings`}
                    compact
                  />
                ) : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </article>
  );
}

function SafeError({ error }: { error: unknown }) {
  return (
    <p className="text-destructive text-sm" role="alert">
      {policyErrorMessage(error)}
    </p>
  );
}

function countPasted(input: string): number {
  return input.trim()
    ? input
        .trim()
        .split(/[\s,]+/)
        .filter(Boolean).length
    : 0;
}

function combineAccountIDs(picked: number[], pasted: string): number[] | Error {
  const tokens = pasted
    .trim()
    .split(/[\s,]+/)
    .filter(Boolean);
  if (tokens.some((token) => !/^[1-9]\d*$/.test(token)))
    return new Error("Account numbers are whole numbers, like 41. Check the pasted list.");
  const pastedIDs = tokens.map(Number);
  if (pastedIDs.some((id) => !Number.isSafeInteger(id)))
    return new Error("One of the pasted account numbers is too large.");
  const ids = [...new Set([...picked, ...pastedIDs])];
  if (!ids.length) return new Error("Choose at least one account.");
  if (ids.length > 10_000) return new Error("Choose 10,000 accounts or fewer.");
  return ids.sort((left, right) => left - right);
}

function snapshotMatchesSelection(response: SnapshotResponse, requestedIDs: number[]): boolean {
  return sameAccountIDs(
    response.items.map((item) => item.account_id),
    requestedIDs,
  );
}

function sameAccountIDs(left: number[], right: number[]): boolean {
  if (left.length !== right.length) return false;
  const sortedLeft = [...left].sort((a, b) => a - b);
  const sortedRight = [...right].sort((a, b) => a - b);
  return sortedLeft.every((value, index) => value === sortedRight[index]);
}

function newIdempotencyKey(): string {
  return typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `policy-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
