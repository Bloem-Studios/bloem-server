import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Clock3, ShieldCheck } from "lucide-react";
import { Link } from "react-router";

import { AdminV2ClientError, adminV2Api } from "@/api/adminV2Client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import type {
  EntitlementCohort,
  PolicyCommand,
  PolicyPatch,
  PolicyPreview,
} from "@/hooks/queries/admin/entitlementCohorts";
import type { PeopleBulkJob, PeopleSelection } from "@/hooks/queries/admin/organizationPeople";

type AccountPolicyState = "managed" | "custom" | "legacy_unmanaged";
type Operation = "" | "assign" | "template" | "derive" | "restore";
type TriState = "unchanged" | "true" | "false";

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
}

interface PatchDraft {
  librariesMode: "unchanged" | "add" | "remove" | "replace" | "all" | "none";
  libraryIDs: string;
  permissionsMode: "unchanged" | "add" | "remove" | "replace" | "unrestricted";
  permissions: string;
  playback: TriState;
  transcode: TriState;
  downloads: TriState;
  transcodedDownloads: TriState;
  requests: TriState;
  maxStreams: string;
  maxProfiles: string;
  maxTranscodes: string;
  maxPlaybackQuality: string;
}

const emptyPatch: PatchDraft = {
  librariesMode: "unchanged",
  libraryIDs: "",
  permissionsMode: "unchanged",
  permissions: "",
  playback: "unchanged",
  transcode: "unchanged",
  downloads: "unchanged",
  transcodedDownloads: "unchanged",
  requests: "unchanged",
  maxStreams: "",
  maxProfiles: "",
  maxTranscodes: "",
  maxPlaybackQuality: "",
};

export default function DirectAccountPolicyBulkPage() {
  useDocumentTitle("Bulk Direct Account Policies");
  const [accountInput, setAccountInput] = useState("");
  const [selectedIDs, setSelectedIDs] = useState<number[]>([]);
  const [snapshotData, setSnapshotData] = useState<SnapshotResponse>();
  const [selectionError, setSelectionError] = useState("");
  const [operation, setOperation] = useState<Operation>("");
  const [cohortID, setCohortID] = useState("");
  const [templateKey, setTemplateKey] = useState("");
  const [templateRevision, setTemplateRevision] = useState("");
  const [derivedName, setDerivedName] = useState("");
  const [patchDraft, setPatchDraft] = useState<PatchDraft>(emptyPatch);
  const [includeCustomProfiles, setIncludeCustomProfiles] = useState(false);
  const [previewBinding, setPreviewBinding] = useState<PreviewBinding>();
  const [previewOutdated, setPreviewOutdated] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob>();
  const [validationError, setValidationError] = useState("");
  const submitGuard = useRef(false);
  const idempotencyKey = useRef("");
  const refreshedTerminalJob = useRef<string | undefined>(undefined);
  const queryClient = useQueryClient();

  const snapshots = useMutation({
    mutationFn: (accountIDs: number[]) =>
      adminV2Api<SnapshotResponse>("/platform/accounts/entitlement-snapshots", {
        method: "POST",
        body: JSON.stringify({ account_ids: accountIDs }),
      }),
    onSuccess: setSnapshotData,
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
    mutationFn: (command: PolicyCommand) =>
      adminV2Api<PreviewResponse>("/platform/accounts/entitlement-bulk/policy-previews", {
        method: "POST",
        body: JSON.stringify({ account_ids: selectedIDs, command }),
      }),
  });

  const createJob = useMutation({
    mutationFn: ({ selection, preview, command }: PreviewBinding) =>
      adminV2Api<{ job: PeopleBulkJob }>("/platform/accounts/entitlement-bulk/policy-jobs", {
        method: "POST",
        body: JSON.stringify({
          selection_token: selection.token,
          confirmation_token: preview.confirmation_token,
          idempotency_key: idempotencyKey.current,
          command,
        }),
      }).then((result) => result.job),
    onSuccess: setSubmittedJob,
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
    mutationFn: (jobID: string) =>
      adminV2Api<{ job: PeopleBulkJob }>(
        `/platform/accounts/entitlement-bulk/policy-jobs/${encodeURIComponent(jobID)}/cancel`,
        { method: "POST", body: "{}" },
      ).then((result) => result.job),
    onSuccess: setSubmittedJob,
  });

  const draftChanges = useMemo(() => describePatch(patchDraft), [patchDraft]);

  function resetPolicyWorkflow() {
    setOperation("");
    setCohortID("");
    setTemplateKey("");
    setTemplateRevision("");
    setDerivedName("");
    setPatchDraft(emptyPatch);
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

  async function reviewSelection() {
    const parsed = parseAccountIDs(accountInput);
    if (parsed instanceof Error) {
      setSelectionError(parsed.message);
      return;
    }
    setSelectionError("");
    setSelectedIDs(parsed);
    setSnapshotData(undefined);
    resetPolicyWorkflow();
    try {
      await snapshots.mutateAsync(parsed);
    } catch {
      // The mutation's bounded API error remains visible below the form.
    }
  }

  function invalidatePreview() {
    if (previewBinding) setPreviewOutdated(true);
    setPreviewBinding(undefined);
    setConfirmed(false);
    setValidationError("");
    createPreview.reset();
    createJob.reset();
    idempotencyKey.current = "";
  }

  function changeOperation(next: Operation) {
    setOperation(next);
    invalidatePreview();
  }

  function changePatch<Key extends keyof PatchDraft>(key: Key, value: PatchDraft[Key]) {
    setPatchDraft((current) => normalizePatch({ ...current, [key]: value }));
    invalidatePreview();
  }

  function buildCommand(): PolicyCommand | undefined {
    setValidationError("");
    if (operation === "assign") {
      if (!cohorts.data?.some((cohort) => cohort.cohort_id === cohortID && !cohort.archived)) {
        setValidationError("Choose an active target cohort.");
        return;
      }
      return {
        kind: "assign_entitlement_cohort",
        cohort_id: cohortID,
        include_custom_profiles: includeCustomProfiles,
      };
    }
    if (operation === "template") {
      const revision = Number(templateRevision);
      if (!templateKey.trim() || !Number.isInteger(revision) || revision <= 0) {
        setValidationError("Enter a template key and positive revision.");
        return;
      }
      return {
        kind: "apply_entitlement_template",
        template_key: templateKey.trim(),
        template_revision: revision,
        include_custom_profiles: includeCustomProfiles,
      };
    }
    if (operation === "restore") {
      return {
        kind: "restore_default_entitlement",
        include_custom_profiles: includeCustomProfiles,
      };
    }
    if (operation === "derive") {
      if (
        !cohorts.data?.some((cohort) => cohort.cohort_id === cohortID && !cohort.archived) ||
        !derivedName.trim()
      ) {
        setValidationError("Choose an active base cohort and name the derived cohort.");
        return;
      }
      const patch = buildPatch(patchDraft);
      if (patch instanceof Error) {
        setValidationError(patch.message);
        return;
      }
      if (Object.keys(patch).length === 0) {
        setValidationError("Change at least one policy field.");
        return;
      }
      return {
        kind: "derive_entitlement_cohort",
        cohort_id: cohortID,
        name: derivedName.trim(),
        patch,
        include_custom_profiles: includeCustomProfiles,
      };
    }
    setValidationError("Choose a policy operation.");
  }

  async function previewImpact() {
    const command = buildCommand();
    if (!command) return;
    try {
      const response = await createPreview.mutateAsync(command);
      setPreviewBinding({ ...response, command });
      setPreviewOutdated(false);
    } catch {
      // The mutation exposes the safe API error.
    }
  }

  async function submitJob() {
    if (submitGuard.current || !confirmed || !previewBinding) return;
    submitGuard.current = true;
    if (!idempotencyKey.current) idempotencyKey.current = newIdempotencyKey();
    refreshedTerminalJob.current = undefined;
    try {
      await createJob.mutateAsync(previewBinding);
    } catch {
      // The mutation exposes the safe API error and retains the reviewed preview.
    } finally {
      submitGuard.current = false;
    }
  }

  const snapshotError = snapshots.error;
  const requestError = createPreview.error ?? createJob.error ?? polledJob.error ?? cancelJob.error;
  const staleConfirmation =
    requestError instanceof AdminV2ClientError &&
    ["policy_confirmation_stale", "selection_expired", "authorization_state_changed"].includes(
      requestError.code,
    );
  const canPreview = Boolean(operation && organizationID) && !createPreview.isPending;

  return (
    <section className="admin-page space-y-6">
      <header className="page-header">
        <div className="space-y-2">
          <p className="text-muted-foreground text-sm">
            <Link
              className="hover:text-foreground underline-offset-4 hover:underline"
              to="/admin/platform/direct-accounts"
            >
              Direct accounts
            </Link>{" "}
            / Bulk policy
          </p>
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Bulk direct-account policies
          </h1>
          <p className="page-subtitle max-w-3xl">
            Review complete Server-observed policies before applying an immutable cohort to an exact
            direct-account selection.
          </p>
        </div>
      </header>

      <section
        className="surface-panel space-y-4 rounded-2xl p-5"
        aria-labelledby="direct-account-selection"
      >
        <div>
          <h2 id="direct-account-selection" className="text-lg font-semibold">
            Select Server accounts
          </h2>
          <p className="text-muted-foreground mt-1 text-sm">
            Enter 1–10,000 positive Server account IDs. The selection never expands from external
            product data or an inferred template.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="direct-account-ids">Server account IDs</Label>
          <textarea
            id="direct-account-ids"
            className="border-input bg-background min-h-24 w-full rounded-md border px-3 py-2 text-sm"
            placeholder="41, 42"
            value={accountInput}
            onChange={(event) => setAccountInput(event.target.value)}
          />
        </div>
        {selectionError ? (
          <p className="text-destructive text-sm" role="alert">
            {selectionError}
          </p>
        ) : null}
        {snapshotError ? <SafeError error={snapshotError} /> : null}
        <Button type="button" disabled={snapshots.isPending} onClick={() => void reviewSelection()}>
          {snapshots.isPending ? "Reading authoritative policies…" : "Review selected accounts"}
        </Button>
      </section>

      {snapshotData ? (
        <section className="space-y-4" aria-labelledby="authoritative-observations">
          <div>
            <h2 id="authoritative-observations" className="text-xl font-semibold">
              Authoritative observations
            </h2>
            <p className="text-muted-foreground mt-1 text-sm">
              Observed together at {formatDate(snapshotData.observed_at)}.
            </p>
          </div>
          <div className="grid gap-4 xl:grid-cols-2">
            {snapshotData.items.map((item) =>
              item.snapshot ? (
                <AccountPolicyCard key={item.account_id} snapshot={item.snapshot} />
              ) : null,
            )}
          </div>
          {incompleteResults.length ? (
            <div
              className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4 text-sm"
              role="alert"
            >
              {incompleteResults.map((item) => (
                <p key={item.account_id}>
                  Account {item.account_id} was {safeResult(item.error)}. No policy operation is
                  available until every explicit target has an authoritative policy.
                </p>
              ))}
            </div>
          ) : !oneOrganization ? (
            <div
              className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4 text-sm"
              role="alert"
            >
              The selected accounts do not resolve to one authoritative direct-account scope.
            </div>
          ) : null}
        </section>
      ) : null}

      {organizationID ? (
        <section
          className="surface-panel space-y-5 rounded-2xl p-5"
          aria-labelledby="policy-operation-heading"
        >
          <div>
            <h2 id="policy-operation-heading" className="text-xl font-semibold">
              Choose exact policy target
            </h2>
            <p className="text-muted-foreground mt-1 text-sm">
              No operation, cohort, or template is selected by default.
            </p>
          </div>
          <fieldset
            className="grid gap-2 md:grid-cols-2"
            role="radiogroup"
            aria-label="Policy operation"
          >
            <OperationChoice
              checked={operation === "assign"}
              label="Move to an existing cohort"
              onChange={() => changeOperation("assign")}
            />
            <OperationChoice
              checked={operation === "template"}
              label="Apply an exact template revision"
              onChange={() => changeOperation("template")}
            />
            <OperationChoice
              checked={operation === "derive"}
              label="Derive a policy for this selection"
              onChange={() => changeOperation("derive")}
            />
            <OperationChoice
              checked={operation === "restore"}
              label="Restore the managed default"
              onChange={() => changeOperation("restore")}
            />
          </fieldset>

          {operation === "assign" || operation === "derive" ? (
            <Field label={operation === "derive" ? "Base cohort" : "Target cohort"}>
              <select
                aria-label={operation === "derive" ? "Base cohort" : "Target cohort"}
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                value={cohortID}
                onChange={(event) => {
                  setCohortID(event.target.value);
                  invalidatePreview();
                }}
              >
                <option value="">Choose a cohort…</option>
                {(cohorts.data ?? []).map((cohort) => (
                  <option key={cohort.cohort_id} value={cohort.cohort_id}>
                    {cohort.name} · revision {cohort.revision}
                  </option>
                ))}
              </select>
            </Field>
          ) : null}
          {cohorts.error ? <SafeError error={cohorts.error} /> : null}

          {operation === "template" ? (
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]">
              <Field label="Template key">
                <Input
                  aria-label="Template key"
                  value={templateKey}
                  onChange={(event) => {
                    setTemplateKey(event.target.value);
                    invalidatePreview();
                  }}
                />
              </Field>
              <Field label="Template revision">
                <Input
                  aria-label="Template revision"
                  type="number"
                  min={1}
                  value={templateRevision}
                  onChange={(event) => {
                    setTemplateRevision(event.target.value);
                    invalidatePreview();
                  }}
                />
              </Field>
            </div>
          ) : null}

          {operation === "derive" ? (
            <DerivedPolicyFields
              draft={patchDraft}
              changes={draftChanges}
              name={derivedName}
              onNameChange={(value) => {
                setDerivedName(value);
                invalidatePreview();
              }}
              onChange={changePatch}
            />
          ) : null}

          {operation === "restore" ? (
            <p className="border-primary/20 bg-primary/5 rounded-lg border p-3 text-sm">
              Every selected account remains attached to an enforceable managed policy.
            </p>
          ) : null}

          {validationError ? (
            <p className="text-destructive text-sm" role="alert">
              {validationError}
            </p>
          ) : null}
          {createPreview.error ? <SafeError error={createPreview.error} /> : null}
          <Button type="button" disabled={!canPreview} onClick={() => void previewImpact()}>
            {previewOutdated ? "Recalculate policy impact" : "Preview policy impact"}
          </Button>
        </section>
      ) : null}

      {previewOutdated && !previewBinding ? (
        <p className="border-border bg-muted/40 rounded-xl border p-4 text-sm" role="status">
          Preview is out of date because a material option changed. Recalculate before confirmation.
        </p>
      ) : null}

      {previewBinding && !visibleJob ? (
        <PreviewAndConfirmation
          binding={previewBinding}
          accountIDs={selectedIDs}
          includeCustomProfiles={includeCustomProfiles}
          confirmed={confirmed}
          submitting={createJob.isPending}
          error={createJob.error}
          onIncludeCustomProfilesChange={(checked) => {
            setIncludeCustomProfiles(checked);
            invalidatePreview();
          }}
          onConfirmedChange={setConfirmed}
          onSubmit={() => void submitJob()}
          onRefresh={() => void reviewSelection()}
          stale={staleConfirmation}
        />
      ) : null}

      {visibleJob ? (
        <JobView
          job={visibleJob}
          error={polledJob.error ?? cancelJob.error}
          cancelling={cancelJob.isPending}
          onCancel={() => void cancelJob.mutateAsync(visibleJob.job_id).catch(() => undefined)}
          onRefresh={() => void reviewSelection()}
        />
      ) : null}
    </section>
  );
}

function AccountPolicyCard({ snapshot }: { snapshot: AccountPolicySnapshot }) {
  return (
    <article
      className="surface-panel space-y-4 rounded-2xl p-5"
      aria-label={`Account ${snapshot.account_id} authoritative policy`}
    >
      <div>
        <h3 className="text-lg font-semibold">Account {snapshot.account_id}</h3>
        <p className="text-muted-foreground text-sm">
          {readableState(snapshot.state)} · Access group {snapshot.group_id} · Policy revision{" "}
          {snapshot.policy_revision}
        </p>
        <p className="text-muted-foreground text-sm">
          {snapshot.source_template_key
            ? `Template ${snapshot.source_template_key} · revision ${snapshot.source_template_revision}`
            : "Template provenance unavailable"}
          {snapshot.cohort_id
            ? ` · Cohort ${snapshot.cohort_id} · Cohort revision ${snapshot.cohort_revision}`
            : " · Cohort provenance unavailable"}
        </p>
      </div>
      <PolicyDetails
        policy={snapshot.policy}
        label={`Account ${snapshot.account_id} effective policy`}
      />
      <section aria-label={`Account ${snapshot.account_id} profiles`} className="space-y-3">
        <h4 className="text-sm font-semibold">Profile policies</h4>
        {snapshot.profiles.map((profile) => (
          <div key={profile.profile_id} className="border-border space-y-2 rounded-lg border p-3">
            <div>
              <p className="text-sm font-medium">{profile.profile_name}</p>
              <p className="text-muted-foreground text-xs">
                {profile.inherits_account ? "Inherits account policy" : "Custom exception"} ·{" "}
                {readableState(profile.state)} · Access group {profile.group_id}
              </p>
            </div>
            <PolicyDetails
              policy={profile.policy}
              label={`${profile.profile_name} effective policy`}
              compact
            />
          </div>
        ))}
      </section>
    </article>
  );
}

function PolicyDetails({
  policy,
  label,
  compact = false,
}: {
  policy: AuthoritativePolicy | PolicyPreview["target"]["policy"];
  label: string;
  compact?: boolean;
}) {
  const audioTranscode =
    "audio_transcode_allowed" in policy ? policy.audio_transcode_allowed : policy.transcode_allowed;
  const values: Array<[string, string]> = [
    [
      "Libraries",
      policy.library_ids === null
        ? "All"
        : policy.library_ids.length
          ? policy.library_ids.join(", ")
          : "None",
    ],
    ["Playback", allowed(policy.playback_allowed)],
    ["Maximum streams", String(policy.max_streams)],
    ["Maximum profiles", String(policy.max_profiles)],
    ["Video transcoding", allowed(policy.transcode_allowed)],
    ["Audio transcoding", allowed(audioTranscode)],
    ["Maximum transcodes", String(policy.max_transcodes)],
    ["Downloads", allowed(policy.download_allowed)],
    ["Transcoded downloads", allowed(policy.download_transcode_allowed)],
    ["Maximum playback quality", policy.max_playback_quality || "Unrestricted"],
    [
      "Permissions",
      policy.allowed_permissions === null
        ? "Unrestricted"
        : policy.allowed_permissions.length
          ? policy.allowed_permissions.join(", ")
          : "None",
    ],
    ["Requests", allowed(policy.requests_allowed)],
  ];
  return (
    <section aria-label={label} className={compact ? "" : "border-border rounded-lg border p-3"}>
      <dl className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-2">
        {values.map(([name, value]) => (
          <div key={name}>
            <dt className="text-muted-foreground inline">{name} </dt>
            <dd className="inline font-medium">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function DerivedPolicyFields({
  draft,
  changes,
  name,
  onNameChange,
  onChange,
}: {
  draft: PatchDraft;
  changes: string[];
  name: string;
  onNameChange(value: string): void;
  onChange<Key extends keyof PatchDraft>(key: Key, value: PatchDraft[Key]): void;
}) {
  const playbackDisabled = draft.playback === "false";
  const downloadsDisabled = playbackDisabled || draft.downloads === "false";
  return (
    <section className="border-border space-y-4 border-t pt-4" aria-label="Derived policy patch">
      <Field label="Derived cohort name">
        <Input
          aria-label="Derived cohort name"
          value={name}
          onChange={(event) => onNameChange(event.target.value)}
        />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <SelectField
          label="Library operation"
          value={draft.librariesMode}
          onChange={(value) => onChange("librariesMode", value as PatchDraft["librariesMode"])}
          options={[
            ["unchanged", "Unchanged"],
            ["add", "Add"],
            ["remove", "Remove"],
            ["replace", "Replace"],
            ["all", "All libraries"],
            ["none", "No libraries"],
          ]}
        />
        <Field label="Library IDs">
          <Input
            aria-label="Library IDs"
            disabled={!["add", "remove", "replace"].includes(draft.librariesMode)}
            value={draft.libraryIDs}
            onChange={(event) => onChange("libraryIDs", event.target.value)}
          />
        </Field>
        <SelectField
          label="Permission operation"
          value={draft.permissionsMode}
          onChange={(value) => onChange("permissionsMode", value as PatchDraft["permissionsMode"])}
          options={[
            ["unchanged", "Unchanged"],
            ["add", "Add"],
            ["remove", "Remove"],
            ["replace", "Replace"],
            ["unrestricted", "Unrestricted"],
          ]}
        />
        <Field label="Permissions">
          <Input
            aria-label="Permissions"
            disabled={!["add", "remove", "replace"].includes(draft.permissionsMode)}
            value={draft.permissions}
            onChange={(event) => onChange("permissions", event.target.value)}
          />
        </Field>
        <TriStateField
          label="Playback"
          value={draft.playback}
          onChange={(value) => onChange("playback", value)}
        />
        <TriStateField
          label="Transcoding"
          value={draft.transcode}
          disabled={playbackDisabled}
          onChange={(value) => onChange("transcode", value)}
        />
        <TriStateField
          label="Downloads"
          value={draft.downloads}
          disabled={playbackDisabled}
          onChange={(value) => onChange("downloads", value)}
        />
        <TriStateField
          label="Transcoded downloads"
          value={draft.transcodedDownloads}
          disabled={downloadsDisabled}
          onChange={(value) => onChange("transcodedDownloads", value)}
        />
        <TriStateField
          label="Requests"
          value={draft.requests}
          onChange={(value) => onChange("requests", value)}
        />
        <NumberField
          label="Maximum streams"
          value={draft.maxStreams}
          disabled={playbackDisabled}
          onChange={(value) => onChange("maxStreams", value)}
        />
        <NumberField
          label="Maximum profiles"
          value={draft.maxProfiles}
          onChange={(value) => onChange("maxProfiles", value)}
        />
        <NumberField
          label="Maximum transcodes"
          value={draft.maxTranscodes}
          disabled={playbackDisabled || draft.transcode === "false"}
          onChange={(value) => onChange("maxTranscodes", value)}
        />
        <Field label="Maximum playback quality">
          <Input
            aria-label="Maximum playback quality"
            disabled={playbackDisabled}
            value={draft.maxPlaybackQuality}
            onChange={(event) => onChange("maxPlaybackQuality", event.target.value)}
          />
        </Field>
      </div>
      <div
        className="border-border bg-muted/30 rounded-lg border p-3"
        aria-label="Exact derived patch"
      >
        <h3 className="text-sm font-semibold">Exact derived patch</h3>
        {changes.length ? (
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {changes.map((change) => (
              <li key={change}>{change}</li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground mt-2 text-sm">No fields changed yet.</p>
        )}
      </div>
    </section>
  );
}

function PreviewAndConfirmation({
  binding,
  accountIDs,
  includeCustomProfiles,
  confirmed,
  submitting,
  error,
  stale,
  onIncludeCustomProfilesChange,
  onConfirmedChange,
  onSubmit,
  onRefresh,
}: {
  binding: PreviewBinding;
  accountIDs: number[];
  includeCustomProfiles: boolean;
  confirmed: boolean;
  submitting: boolean;
  error: Error | null;
  stale: boolean;
  onIncludeCustomProfilesChange(value: boolean): void;
  onConfirmedChange(value: boolean): void;
  onSubmit(): void;
  onRefresh(): void;
}) {
  const { preview, selection } = binding;
  return (
    <section
      className="surface-panel space-y-5 rounded-2xl p-5"
      aria-labelledby="authoritative-impact"
    >
      <div>
        <h2 id="authoritative-impact" className="text-xl font-semibold">
          Review authoritative impact
        </h2>
        <p className="text-muted-foreground mt-1 text-sm">
          This confirmation binds {selection.matched} exact Server accounts until{" "}
          {formatDate(preview.confirmation_expires_at)}.
        </p>
        <p className="mt-2 text-sm font-medium">Accounts {accountIDs.join(", ")}</p>
      </div>
      <label className="border-border flex items-start gap-3 rounded-lg border p-3 text-sm">
        <input
          type="checkbox"
          aria-label="Move custom profiles too"
          checked={includeCustomProfiles}
          onChange={(event) => onIncludeCustomProfilesChange(event.target.checked)}
        />
        <span>
          <span className="font-medium">Move custom profiles too</span>
          <span className="text-muted-foreground block">
            Off by default. Inherited profiles always move.
          </span>
        </span>
      </label>
      <dl className="grid gap-3 sm:grid-cols-5">
        <Metric label="Matched" value={preview.matched} />
        <Metric label="Excluded" value={preview.excluded} />
        <Metric label="Already compliant" value={preview.already_compliant} />
        <Metric label="Inherited profiles move" value={preview.inherited_profiles_will_move} />
        <Metric label="Stale or ineligible" value={preview.ineligible_or_stale} />
      </dl>
      <p className="text-sm">
        {preview.custom_profiles_will_move
          ? `${preview.custom_profiles_will_move} custom profiles move`
          : `${preview.custom_profiles_will_remain} custom profiles remain unchanged`}
      </p>
      <section aria-label="Observed policy differences">
        <h3 className="text-sm font-semibold">Observed policy differences</h3>
        <ul className="text-muted-foreground mt-2 text-sm">
          {preview.diff.map((diff) => (
            <li key={diff.field}>
              {diff.changed_accounts} accounts change {readableField(diff.field)}
            </li>
          ))}
        </ul>
      </section>
      <section
        className="border-primary/20 bg-primary/5 rounded-lg border p-4"
        aria-label="Exact policy target"
      >
        <div className="flex items-center gap-2">
          <ShieldCheck className="size-4" aria-hidden />
          <h3 className="text-sm font-semibold">Exact policy target</h3>
        </div>
        <p className="text-muted-foreground mt-2 text-sm">
          {readableField(preview.target.kind)} · digest {preview.target.policy_digest}
        </p>
        {preview.target.template_key ? (
          <p className="text-muted-foreground text-sm">
            Template {preview.target.template_key} · revision {preview.target.template_revision}
          </p>
        ) : null}
        {preview.target.cohort_id ? (
          <p className="text-muted-foreground text-sm">
            Cohort {preview.target.cohort_id} · revision {preview.target.cohort_revision}
          </p>
        ) : null}
      </section>
      <PolicyDetails policy={preview.target.policy} label="Authoritative target policy" />
      <label className="border-primary/30 bg-primary/5 flex items-start gap-3 rounded-lg border p-4 text-sm">
        <input
          type="checkbox"
          aria-label="I confirm this exact account set and policy target"
          checked={confirmed}
          onChange={(event) => onConfirmedChange(event.target.checked)}
        />
        <span>
          <span className="font-semibold">I confirm this exact account set and policy target</span>
          <span className="text-muted-foreground block">
            Stale accounts are skipped safely and no account becomes policy-less.
          </span>
        </span>
      </label>
      {error ? <SafeError error={error} /> : null}
      {stale ? (
        <Button type="button" variant="outline" onClick={onRefresh}>
          Refresh authoritative selection
        </Button>
      ) : null}
      <Button type="button" disabled={!confirmed || submitting} onClick={onSubmit}>
        Start policy job
      </Button>
    </section>
  );
}

function JobView({
  job,
  error,
  cancelling,
  onCancel,
  onRefresh,
}: {
  job: PeopleBulkJob;
  error: Error | null;
  cancelling: boolean;
  onCancel(): void;
  onRefresh(): void;
}) {
  const pending = job.status === "queued" || job.status === "running";
  const Icon = pending ? Clock3 : job.status === "completed" ? CheckCircle2 : AlertTriangle;
  const progress = job.progress_total ? (job.progress_current / job.progress_total) * 100 : 0;
  return (
    <section className="surface-panel space-y-5 rounded-2xl p-5" aria-live="polite">
      <div className="flex items-start gap-3">
        <Icon className="text-muted-foreground mt-0.5 size-5" aria-hidden />
        <div>
          <h2 className="text-xl font-semibold">Policy job {job.status}</h2>
          <p className="text-muted-foreground text-sm">Job {job.job_id}</p>
        </div>
      </div>
      <Progress value={progress} aria-label="Policy job progress" />
      <p className="text-muted-foreground text-sm">
        {job.progress_current} of {job.progress_total} processed
      </p>
      <dl className="grid grid-cols-3 gap-3">
        <Metric label="Succeeded" value={job.succeeded} />
        <Metric label="Skipped" value={job.skipped.length} />
        <Metric label="Failed" value={job.failed.length} />
      </dl>
      {job.skipped.length || job.failed.length ? (
        <ul className="text-muted-foreground text-sm">
          {[...job.skipped, ...job.failed].map((result, index) => (
            <li key={`${result.account_id}:${result.reason}:${index}`}>
              Account {result.account_id} — {readableField(result.reason)}
            </li>
          ))}
        </ul>
      ) : null}
      {error ? <SafeError error={error} /> : null}
      <div className="flex gap-2">
        {pending ? (
          <Button type="button" variant="destructive" disabled={cancelling} onClick={onCancel}>
            Cancel policy job
          </Button>
        ) : null}
        {!pending || job.skipped.length || job.failed.length ? (
          <Button type="button" variant="outline" onClick={onRefresh}>
            Refresh authoritative selection
          </Button>
        ) : null}
      </div>
    </section>
  );
}

function OperationChoice({
  checked,
  label,
  onChange,
}: {
  checked: boolean;
  label: string;
  onChange(): void;
}) {
  return (
    <label className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 flex cursor-pointer gap-3 rounded-lg border p-3">
      <input
        type="radio"
        name="direct-policy-operation"
        aria-label={label}
        checked={checked}
        onChange={onChange}
      />
      <span className="text-sm font-medium">{label}</span>
    </label>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

function SelectField({
  label,
  value,
  options,
  disabled = false,
  onChange,
}: {
  label: string;
  value: string;
  options: Array<[string, string]>;
  disabled?: boolean;
  onChange(value: string): void;
}) {
  return (
    <Field label={label}>
      <select
        aria-label={label}
        className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </select>
    </Field>
  );
}

function TriStateField({
  label,
  value,
  disabled = false,
  onChange,
}: {
  label: string;
  value: TriState;
  disabled?: boolean;
  onChange(value: TriState): void;
}) {
  return (
    <SelectField
      label={label}
      value={value}
      disabled={disabled}
      onChange={(next) => onChange(next as TriState)}
      options={[
        ["unchanged", "Unchanged"],
        ["true", "Enabled"],
        ["false", "Disabled"],
      ]}
    />
  );
}

function NumberField({
  label,
  value,
  disabled = false,
  onChange,
}: {
  label: string;
  value: string;
  disabled?: boolean;
  onChange(value: string): void;
}) {
  return (
    <Field label={label}>
      <Input
        aria-label={label}
        type="number"
        min={0}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </Field>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="border-border bg-muted/30 rounded-lg border p-3">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="mt-1 font-semibold">{value.toLocaleString()}</dd>
    </div>
  );
}

function SafeError({ error }: { error: unknown }) {
  return (
    <p className="text-destructive text-sm" role="alert">
      {safeErrorMessage(error)}
    </p>
  );
}

function parseAccountIDs(input: string): number[] | Error {
  const tokens = input
    .trim()
    .split(/[\s,]+/)
    .filter(Boolean);
  if (!tokens.length) return new Error("Enter at least one Server account ID.");
  if (tokens.length > 10_000) return new Error("Select no more than 10,000 Server accounts.");
  if (tokens.some((token) => !/^[1-9]\d*$/.test(token)))
    return new Error("Server account IDs must be positive whole numbers.");
  const ids = tokens.map(Number);
  if (ids.some((id) => !Number.isSafeInteger(id)))
    return new Error("Server account IDs must be positive safe integers.");
  if (new Set(ids).size !== ids.length)
    return new Error("Remove duplicate Server account IDs before review.");
  return [...ids].sort((left, right) => left - right);
}

function buildPatch(draft: PatchDraft): PolicyPatch | Error {
  draft = normalizePatch(draft);
  const patch: PolicyPatch = {};
  if (draft.librariesMode !== "unchanged") {
    if (["add", "remove", "replace"].includes(draft.librariesMode)) {
      const values = parsePositiveList(
        draft.libraryIDs,
        "Library operations require positive numeric library IDs.",
      );
      if (values instanceof Error) return values;
      patch.libraries = { mode: draft.librariesMode as "add" | "remove" | "replace", values };
    } else patch.libraries = { mode: draft.librariesMode };
  }
  if (draft.permissionsMode !== "unchanged") {
    if (["add", "remove", "replace"].includes(draft.permissionsMode)) {
      const values = [
        ...new Set(
          draft.permissions
            .split(",")
            .map((item) => item.trim())
            .filter(Boolean),
        ),
      ].sort();
      if (!values.length)
        return new Error("Permission operations require at least one permission.");
      patch.permissions = { mode: draft.permissionsMode as "add" | "remove" | "replace", values };
    } else patch.permissions = { mode: "unrestricted" };
  }
  const booleans: Array<
    [
      TriState,
      (
        | "playback_allowed"
        | "transcode_allowed"
        | "download_allowed"
        | "download_transcode_allowed"
        | "requests_allowed"
      ),
    ]
  > = [
    [draft.playback, "playback_allowed"],
    [draft.transcode, "transcode_allowed"],
    [draft.downloads, "download_allowed"],
    [draft.transcodedDownloads, "download_transcode_allowed"],
    [draft.requests, "requests_allowed"],
  ];
  for (const [value, key] of booleans) if (value !== "unchanged") patch[key] = value === "true";
  const numbers: Array<[string, "max_streams" | "max_profiles" | "max_transcodes", string]> = [
    [draft.maxStreams, "max_streams", "Maximum streams"],
    [draft.maxProfiles, "max_profiles", "Maximum profiles"],
    [draft.maxTranscodes, "max_transcodes", "Maximum transcodes"],
  ];
  for (const [value, key, label] of numbers) {
    if (!value) continue;
    const parsed = Number(value);
    if (!Number.isInteger(parsed) || parsed < 0)
      return new Error(`${label} must be a non-negative whole number.`);
    patch[key] = parsed;
  }
  if (draft.playback === "false") patch.max_playback_quality = "";
  else if (draft.maxPlaybackQuality.trim())
    patch.max_playback_quality = draft.maxPlaybackQuality.trim();
  return patch;
}

function normalizePatch(draft: PatchDraft): PatchDraft {
  const next = { ...draft };
  if (next.playback === "false")
    return {
      ...next,
      maxStreams: "0",
      transcode: "false",
      maxTranscodes: "0",
      downloads: "false",
      transcodedDownloads: "false",
      maxPlaybackQuality: "",
    };
  if (next.transcode === "false") next.maxTranscodes = "0";
  if (next.downloads === "false") next.transcodedDownloads = "false";
  return next;
}

function describePatch(draft: PatchDraft): string[] {
  const values: string[] = [];
  if (draft.librariesMode !== "unchanged")
    values.push(
      `Libraries: ${draft.librariesMode}${draft.libraryIDs.trim() ? ` ${draft.libraryIDs.trim()}` : ""}`,
    );
  if (draft.permissionsMode !== "unchanged")
    values.push(
      `Permissions: ${draft.permissionsMode}${draft.permissions.trim() ? ` ${draft.permissions.trim()}` : ""}`,
    );
  const booleans: Array<[string, TriState]> = [
    ["Playback", draft.playback],
    ["Transcoding", draft.transcode],
    ["Downloads", draft.downloads],
    ["Transcoded downloads", draft.transcodedDownloads],
    ["Requests", draft.requests],
  ];
  for (const [label, value] of booleans)
    if (value !== "unchanged")
      values.push(`${label}: ${value === "true" ? "enabled" : "disabled"}`);
  const fields: Array<[string, string]> = [
    ["Maximum streams", draft.maxStreams],
    ["Maximum profiles", draft.maxProfiles],
    ["Maximum transcodes", draft.maxTranscodes],
    ["Maximum playback quality", draft.maxPlaybackQuality],
  ];
  for (const [label, value] of fields) if (value.trim()) values.push(`${label}: ${value.trim()}`);
  return values;
}

function parsePositiveList(input: string, message: string): number[] | Error {
  const tokens = input
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean);
  if (!tokens.length || tokens.some((value) => !/^[1-9]\d*$/.test(value)))
    return new Error(message);
  return [...new Set(tokens.map(Number))].sort((left, right) => left - right);
}

function safeErrorMessage(error: unknown): string {
  if (error instanceof AdminV2ClientError) {
    if (error.code === "policy_confirmation_stale")
      return "The policy preview changed or expired. Refresh the authoritative selection and preview again.";
    if (error.code === "selection_expired")
      return "The immutable account selection expired. Refresh the authoritative selection.";
    if (error.code === "authorization_state_changed")
      return "Administrative authority changed. Refresh the authoritative selection.";
    if (error.code === "not_found")
      return "One or more entitlement resources were not found in this direct-account scope.";
    if (error.code === "idempotency_conflict")
      return "A different command already used this submission key. Review and submit again.";
    if (error.code === "job_not_cancellable") return "This policy job can no longer be cancelled.";
    if (error.code === "entitlements_unavailable")
      return "Entitlement policy service is temporarily unavailable. Try again.";
    if (error.code === "validation_failed")
      return "The policy request is invalid. Review the exact account set and operation.";
  }
  return "The policy request could not be completed. Try again.";
}

function safeResult(value?: string): string {
  return value === "not_found"
    ? "not found in this direct-account scope"
    : value === "stale"
      ? "stale"
      : "unavailable";
}
function readableState(value: string): string {
  return value.replace(/_/g, " ");
}
function readableField(value: string): string {
  return value.replace(/_/g, " ");
}
function allowed(value: boolean): string {
  return value ? "Allowed" : "Not allowed";
}
function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}
function newIdempotencyKey(): string {
  return typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `policy-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
