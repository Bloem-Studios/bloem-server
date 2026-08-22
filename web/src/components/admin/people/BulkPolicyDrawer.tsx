import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, CheckCircle2, Clock3, ShieldCheck } from "lucide-react";

import { AdminV2ClientError } from "@/api/adminV2Client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import type { AdminContextKey } from "@/api/types";
import {
  useCancelPolicyJob,
  useCreatePolicyJob,
  useCreatePolicyPreview,
  usePolicyJob,
  type EffectivePolicy,
  type EntitlementCohort,
  type PolicyCommand,
  type PolicyPatch,
  type PolicyPreview,
} from "@/hooks/queries/admin/entitlementCohorts";
import type { PeopleBulkJob, PeopleSelection } from "@/hooks/queries/admin/organizationPeople";

type Operation = "assign" | "template" | "derive" | "restore";
type TriState = "unchanged" | "true" | "false";

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

const initialPatchDraft: PatchDraft = {
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

export default function BulkPolicyDrawer({
  open,
  contextKey,
  organizationName,
  selection,
  cohorts,
  initialCohortID,
  onOpenChange,
  onRetrySelection,
}: {
  open: boolean;
  contextKey: AdminContextKey;
  organizationName: string;
  selection: PeopleSelection;
  cohorts: EntitlementCohort[];
  initialCohortID?: string;
  onOpenChange(open: boolean): void;
  onRetrySelection(): void;
}) {
  const [step, setStep] = useState(1);
  const [operation, setOperation] = useState<Operation>("assign");
  const [cohortID, setCohortID] = useState(initialCohortID ?? cohorts[0]?.cohort_id ?? "");
  const [templateKey, setTemplateKey] = useState("");
  const [templateRevision, setTemplateRevision] = useState("");
  const [derivedName, setDerivedName] = useState("");
  const [patchDraft, setPatchDraft] = useState<PatchDraft>(initialPatchDraft);
  const [includeCustomProfiles, setIncludeCustomProfiles] = useState(false);
  const [preview, setPreview] = useState<PolicyPreview>();
  const [confirmed, setConfirmed] = useState(false);
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob>();
  const [validationError, setValidationError] = useState("");
  const previewGuard = useRef(false);
  const submitGuard = useRef(false);
  const cancelGuard = useRef(false);
  const idempotencyKey = useRef("");
  const createPreview = useCreatePolicyPreview(contextKey);
  const createJob = useCreatePolicyJob(contextKey);
  const polledJob = usePolicyJob(contextKey, submittedJob?.job_id);
  const cancelJob = useCancelPolicyJob(contextKey);
  const visibleJob = polledJob.data ?? submittedJob;
  const draftChanges = useMemo(() => describePatchDraft(patchDraft), [patchDraft]);

  useEffect(() => {
    setStep(1);
    setPreview(undefined);
    setConfirmed(false);
    setSubmittedJob(undefined);
    setValidationError("");
    idempotencyKey.current = "";
    submitGuard.current = false;
  }, [selection.token, contextKey]);

  useEffect(() => {
    if (initialCohortID && cohorts.some((cohort) => cohort.cohort_id === initialCohortID)) {
      setCohortID(initialCohortID);
    } else if (!cohortID && cohorts[0]) {
      setCohortID(cohorts[0].cohort_id);
    }
  }, [cohortID, cohorts, initialCohortID]);

  function clearPreview() {
    setPreview(undefined);
    setConfirmed(false);
    setValidationError("");
    createPreview.reset();
    createJob.reset();
    idempotencyKey.current = "";
  }

  function changeOperation(next: Operation) {
    setOperation(next);
    clearPreview();
  }

  function changePatch<Key extends keyof PatchDraft>(key: Key, value: PatchDraft[Key]) {
    setPatchDraft((current) => normalizePatchDraft({ ...current, [key]: value }));
    clearPreview();
  }

  function buildCommand(): PolicyCommand | undefined {
    setValidationError("");
    if (operation === "assign") {
      if (!cohortID) {
        setValidationError("Choose a target cohort.");
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
    if (!cohortID || !derivedName.trim()) {
      setValidationError("Choose a base cohort and name the derived cohort.");
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

  async function previewImpact() {
    if (previewGuard.current) return;
    const command = buildCommand();
    if (!command) return;
    previewGuard.current = true;
    try {
      const result = await createPreview.mutateAsync({
        selectionToken: selection.token,
        command,
      });
      setPreview(result);
      setStep(3);
    } catch {
      // The mutation exposes only the safe API error rendered by this drawer.
    } finally {
      previewGuard.current = false;
    }
  }

  async function submit() {
    if (submitGuard.current || !preview || !confirmed) return;
    const command = buildCommand();
    if (!command) return;
    submitGuard.current = true;
    if (!idempotencyKey.current) idempotencyKey.current = newIdempotencyKey();
    try {
      const job = await createJob.mutateAsync({
        selectionToken: selection.token,
        confirmationToken: preview.confirmation_token,
        idempotencyKey: idempotencyKey.current,
        command,
      });
      setSubmittedJob(job);
    } catch {
      // The mutation error remains visible in the confirmation step.
    } finally {
      submitGuard.current = false;
    }
  }

  async function cancel() {
    if (!visibleJob || cancelGuard.current) return;
    cancelGuard.current = true;
    try {
      const cancelled = await cancelJob.mutateAsync(visibleJob.job_id);
      setSubmittedJob(cancelled);
    } catch {
      // The job view renders a bounded safe error and keeps polling status.
    } finally {
      cancelGuard.current = false;
    }
  }

  const requestError = createPreview.error ?? createJob.error ?? polledJob.error ?? cancelJob.error;
  const canRetrySelection =
    requestError instanceof AdminV2ClientError &&
    ["selection_expired", "policy_confirmation_stale"].includes(requestError.code);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full gap-0 overflow-hidden sm:max-w-2xl">
        <SheetHeader className="border-border border-b px-5 py-4 pr-12">
          <SheetTitle>Apply entitlement policy</SheetTitle>
          <SheetDescription>
            A reviewed immutable selection in {organizationName}. Current policy is always read from
            the server.
          </SheetDescription>
          <WorkflowSteps current={step} job={Boolean(visibleJob)} />
        </SheetHeader>

        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          {visibleJob ? (
            <JobView
              job={visibleJob}
              error={requestError}
              cancelling={cancelJob.isPending}
              onCancel={() => void cancel()}
              onRetrySelection={onRetrySelection}
            />
          ) : step === 1 ? (
            <SelectionStep selection={selection} organizationName={organizationName} />
          ) : step === 2 ? (
            <OperationStep
              operation={operation}
              onOperationChange={changeOperation}
              cohorts={cohorts}
              cohortID={cohortID}
              onCohortChange={(value) => {
                setCohortID(value);
                clearPreview();
              }}
              templateKey={templateKey}
              onTemplateKeyChange={(value) => {
                setTemplateKey(value);
                clearPreview();
              }}
              templateRevision={templateRevision}
              onTemplateRevisionChange={(value) => {
                setTemplateRevision(value);
                clearPreview();
              }}
              derivedName={derivedName}
              onDerivedNameChange={(value) => {
                setDerivedName(value);
                clearPreview();
              }}
              patchDraft={patchDraft}
              onPatchChange={changePatch}
              draftChanges={draftChanges}
              validationError={validationError}
            />
          ) : step === 3 ? (
            <ImpactStep
              preview={preview}
              includeCustomProfiles={includeCustomProfiles}
              onIncludeCustomProfilesChange={(checked) => {
                setIncludeCustomProfiles(checked);
                clearPreview();
              }}
              error={createPreview.error}
              onRetrySelection={onRetrySelection}
            />
          ) : (
            <ConfirmationStep
              selection={selection}
              preview={preview!}
              includeCustomProfiles={includeCustomProfiles}
              confirmed={confirmed}
              onConfirmedChange={setConfirmed}
              error={createJob.error}
            />
          )}
        </div>

        {!visibleJob ? (
          <SheetFooter className="border-border flex-row justify-between border-t px-5 py-4">
            {step > 1 ? (
              <Button
                type="button"
                variant="outline"
                onClick={() => setStep((current) => Math.max(1, current - 1))}
              >
                Back
              </Button>
            ) : (
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
            )}
            {step === 1 ? (
              <Button type="button" onClick={() => setStep(2)}>
                Choose policy operation
              </Button>
            ) : step === 2 ? (
              <Button
                type="button"
                disabled={createPreview.isPending}
                onClick={() => void previewImpact()}
              >
                Preview policy impact
              </Button>
            ) : step === 3 ? (
              preview ? (
                <Button type="button" onClick={() => setStep(4)}>
                  Continue to confirmation
                </Button>
              ) : (
                <Button
                  type="button"
                  disabled={createPreview.isPending}
                  onClick={() => void previewImpact()}
                >
                  Recalculate policy impact
                </Button>
              )
            ) : (
              <Button
                type="button"
                disabled={!confirmed || createJob.isPending}
                onClick={() => void submit()}
              >
                Start policy job
              </Button>
            )}
          </SheetFooter>
        ) : null}

        {requestError && !visibleJob && step !== 3 && step !== 4 ? (
          <div
            className="border-destructive/30 bg-destructive/10 m-5 mt-0 rounded-lg border p-3"
            role="alert"
          >
            <p className="text-destructive text-sm">{safeErrorMessage(requestError)}</p>
            {canRetrySelection ? (
              <Button className="mt-3" size="sm" variant="outline" onClick={onRetrySelection}>
                Refresh selection and retry
              </Button>
            ) : null}
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function WorkflowSteps({ current, job }: { current: number; job: boolean }) {
  const labels = ["Selection", "Operation", "Impact", "Confirmation"];
  return (
    <ol aria-label="Policy workflow steps" className="mt-3 grid grid-cols-4 gap-2 text-xs">
      {labels.map((label, index) => {
        const number = index + 1;
        const active = !job && number === current;
        return (
          <li
            key={label}
            aria-current={active ? "step" : undefined}
            className={active ? "text-foreground font-semibold" : "text-muted-foreground"}
          >
            <span className="bg-muted mr-1 inline-grid size-5 place-items-center rounded-full">
              {number}
            </span>
            <span className="hidden sm:inline">{label}</span>
          </li>
        );
      })}
    </ol>
  );
}

function SelectionStep({
  selection,
  organizationName,
}: {
  selection: PeopleSelection;
  organizationName: string;
}) {
  return (
    <section className="space-y-5">
      <div>
        <h2 className="text-lg font-semibold">Review selection</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          This snapshot will not silently expand if filters or membership change.
        </p>
      </div>
      <dl className="grid gap-3 sm:grid-cols-3">
        <Metric label="Organization" value={organizationName} />
        <Metric label="Included" value={`${selection.matched.toLocaleString()} matched`} />
        <Metric label="Excluded" value={`${selection.excluded.toLocaleString()} excluded`} />
      </dl>
      <p className="border-border bg-muted/40 rounded-lg border p-3 text-sm">
        Selection expires {formatDate(selection.expires_at)}. A later policy preview will bind this
        exact snapshot to the chosen operation.
      </p>
    </section>
  );
}

function OperationStep({
  operation,
  onOperationChange,
  cohorts,
  cohortID,
  onCohortChange,
  templateKey,
  onTemplateKeyChange,
  templateRevision,
  onTemplateRevisionChange,
  derivedName,
  onDerivedNameChange,
  patchDraft,
  onPatchChange,
  draftChanges,
  validationError,
}: {
  operation: Operation;
  onOperationChange(value: Operation): void;
  cohorts: EntitlementCohort[];
  cohortID: string;
  onCohortChange(value: string): void;
  templateKey: string;
  onTemplateKeyChange(value: string): void;
  templateRevision: string;
  onTemplateRevisionChange(value: string): void;
  derivedName: string;
  onDerivedNameChange(value: string): void;
  patchDraft: PatchDraft;
  onPatchChange<Key extends keyof PatchDraft>(key: Key, value: PatchDraft[Key]): void;
  draftChanges: string[];
  validationError: string;
}) {
  const playbackDisabled = patchDraft.playback === "false";
  const transcodeDisabled = playbackDisabled || patchDraft.transcode === "false";
  const downloadDisabled = playbackDisabled || patchDraft.downloads === "false";
  return (
    <section className="space-y-5">
      <div>
        <h2 className="text-lg font-semibold">Choose policy operation</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          Cohorts are immutable. Selection-specific changes create a derived revision.
        </p>
      </div>
      <fieldset className="grid gap-2" role="radiogroup" aria-label="Policy operation">
        <OperationChoice
          checked={operation === "assign"}
          label="Move to an existing cohort"
          description="Use the cohort’s complete observed policy."
          onChange={() => onOperationChange("assign")}
        />
        <OperationChoice
          checked={operation === "template"}
          label="Apply an exact template revision"
          description="Resolve and reuse the exact global template revision."
          onChange={() => onOperationChange("template")}
        />
        <OperationChoice
          checked={operation === "derive"}
          label="Derive a policy for this selection"
          description="Create a new immutable cohort without changing its parent."
          onChange={() => onOperationChange("derive")}
        />
        <OperationChoice
          checked={operation === "restore"}
          label="Restore the managed default"
          description="Keep every selected account attached to an enforceable policy."
          onChange={() => onOperationChange("restore")}
        />
      </fieldset>

      {(operation === "assign" || operation === "derive") && (
        <Field label={operation === "derive" ? "Base cohort" : "Target cohort"}>
          <select
            id={operation === "derive" ? "base-cohort" : "target-cohort"}
            aria-label={operation === "derive" ? "Base cohort" : "Target cohort"}
            className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
            value={cohortID}
            onChange={(event) => onCohortChange(event.target.value)}
          >
            <option value="">Choose a cohort…</option>
            {cohorts.map((cohort) => (
              <option key={cohort.cohort_id} value={cohort.cohort_id}>
                {cohort.name} · revision {cohort.revision}
              </option>
            ))}
          </select>
        </Field>
      )}

      {operation === "template" && (
        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem]">
          <Field label="Template key">
            <Input
              id="template-key"
              aria-label="Template key"
              value={templateKey}
              onChange={(event) => onTemplateKeyChange(event.target.value)}
            />
          </Field>
          <Field label="Template revision">
            <Input
              id="template-revision"
              aria-label="Template revision"
              type="number"
              min={1}
              value={templateRevision}
              onChange={(event) => onTemplateRevisionChange(event.target.value)}
            />
          </Field>
        </div>
      )}

      {operation === "derive" && (
        <div className="space-y-4 border-t pt-4">
          <Field label="Derived cohort name">
            <Input
              id="derived-cohort-name"
              aria-label="Derived cohort name"
              value={derivedName}
              onChange={(event) => onDerivedNameChange(event.target.value)}
            />
          </Field>
          <div className="grid gap-3 sm:grid-cols-[10rem_minmax(0,1fr)]">
            <SelectField
              label="Library operation"
              value={patchDraft.librariesMode}
              onChange={(value) =>
                onPatchChange("librariesMode", value as PatchDraft["librariesMode"])
              }
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
                id="library-ids"
                aria-label="Library IDs"
                placeholder="12, 18"
                disabled={!["add", "remove", "replace"].includes(patchDraft.librariesMode)}
                value={patchDraft.libraryIDs}
                onChange={(event) => onPatchChange("libraryIDs", event.target.value)}
              />
            </Field>
          </div>
          <div className="grid gap-3 sm:grid-cols-[10rem_minmax(0,1fr)]">
            <SelectField
              label="Permission operation"
              value={patchDraft.permissionsMode}
              onChange={(value) =>
                onPatchChange("permissionsMode", value as PatchDraft["permissionsMode"])
              }
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
                id="permissions"
                aria-label="Permissions"
                placeholder="request_media"
                disabled={!["add", "remove", "replace"].includes(patchDraft.permissionsMode)}
                value={patchDraft.permissions}
                onChange={(event) => onPatchChange("permissions", event.target.value)}
              />
            </Field>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <TriStateField
              label="Playback"
              value={patchDraft.playback}
              onChange={(value) => onPatchChange("playback", value)}
            />
            <TriStateField
              label="Transcoding"
              value={patchDraft.transcode}
              disabled={playbackDisabled}
              onChange={(value) => onPatchChange("transcode", value)}
            />
            <TriStateField
              label="Downloads"
              value={patchDraft.downloads}
              disabled={playbackDisabled}
              onChange={(value) => onPatchChange("downloads", value)}
            />
            <TriStateField
              label="Transcoded downloads"
              value={patchDraft.transcodedDownloads}
              disabled={downloadDisabled}
              onChange={(value) => onPatchChange("transcodedDownloads", value)}
            />
            <TriStateField
              label="Requests"
              value={patchDraft.requests}
              onChange={(value) => onPatchChange("requests", value)}
            />
            <NumberField
              label="Maximum profiles"
              value={patchDraft.maxProfiles}
              onChange={(value) => onPatchChange("maxProfiles", value)}
            />
            <NumberField
              label="Maximum streams"
              value={patchDraft.maxStreams}
              disabled={playbackDisabled}
              onChange={(value) => onPatchChange("maxStreams", value)}
            />
            <NumberField
              label="Maximum transcodes"
              value={patchDraft.maxTranscodes}
              disabled={transcodeDisabled}
              onChange={(value) => onPatchChange("maxTranscodes", value)}
            />
            <Field label="Maximum playback quality">
              <Input
                id="maximum-playback-quality"
                aria-label="Maximum playback quality"
                disabled={playbackDisabled}
                value={patchDraft.maxPlaybackQuality}
                onChange={(event) => onPatchChange("maxPlaybackQuality", event.target.value)}
              />
            </Field>
          </div>
          <section
            className="border-border bg-muted/30 rounded-lg border p-3"
            aria-label="Draft policy changes"
          >
            <h3 className="text-sm font-semibold">Draft policy changes</h3>
            {draftChanges.length ? (
              <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
                {draftChanges.map((change) => (
                  <li key={change}>{change}</li>
                ))}
              </ul>
            ) : (
              <p className="text-muted-foreground mt-2 text-sm">No fields changed yet.</p>
            )}
          </section>
        </div>
      )}
      {validationError ? (
        <p className="text-destructive text-sm" role="alert">
          {validationError}
        </p>
      ) : null}
    </section>
  );
}

function ImpactStep({
  preview,
  includeCustomProfiles,
  onIncludeCustomProfilesChange,
  error,
  onRetrySelection,
}: {
  preview?: PolicyPreview;
  includeCustomProfiles: boolean;
  onIncludeCustomProfilesChange(checked: boolean): void;
  error: Error | null;
  onRetrySelection(): void;
}) {
  const retrySelection =
    error instanceof AdminV2ClientError &&
    ["selection_expired", "policy_confirmation_stale"].includes(error.code);
  return (
    <section className="space-y-5">
      <div>
        <h2 className="text-lg font-semibold">Review impact</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          Counts and target policy below are authoritative server observations.
        </p>
      </div>
      <label className="border-border flex items-start gap-3 rounded-lg border p-3 text-sm">
        <input
          type="checkbox"
          aria-label="Move custom profiles too"
          className="mt-0.5 size-4"
          checked={includeCustomProfiles}
          onChange={(event) => onIncludeCustomProfilesChange(event.target.checked)}
        />
        <span>
          <span className="font-medium">Move custom profiles too</span>
          <span className="text-muted-foreground mt-0.5 block">
            Off by default. Inherited profiles always move with the account.
          </span>
        </span>
      </label>
      {!preview && !error ? (
        <p className="border-border bg-muted/40 rounded-lg border p-3 text-sm" role="status">
          Preview is out of date because a material option changed. Recalculate before continuing.
        </p>
      ) : null}
      {error ? (
        <div className="border-destructive/30 bg-destructive/10 rounded-lg border p-3" role="alert">
          <p className="text-destructive text-sm">{safeErrorMessage(error)}</p>
          {retrySelection ? (
            <Button className="mt-3" size="sm" variant="outline" onClick={onRetrySelection}>
              Refresh selection and retry
            </Button>
          ) : null}
        </div>
      ) : null}
      {preview ? (
        <>
          <dl className="grid gap-3 sm:grid-cols-2">
            <Metric label="Already compliant" value={preview.already_compliant.toLocaleString()} />
            <Metric
              label="Stale or ineligible"
              value={preview.ineligible_or_stale.toLocaleString()}
            />
            <Metric
              label="Inherited profiles"
              value={`${preview.inherited_profiles_will_move.toLocaleString()} inherited profiles move`}
            />
            <Metric
              label="Custom profiles"
              value={
                preview.custom_profiles_will_move
                  ? `${preview.custom_profiles_will_move.toLocaleString()} custom profiles move`
                  : `${preview.custom_profiles_will_remain.toLocaleString()} custom profiles remain unchanged`
              }
            />
          </dl>
          <section aria-label="Policy differences">
            <h3 className="text-sm font-semibold">Observed policy differences</h3>
            <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
              {preview.diff.map((item) => (
                <li key={item.field}>
                  {item.changed_accounts.toLocaleString()} accounts change{" "}
                  {readableField(item.field)}
                </li>
              ))}
            </ul>
          </section>
          <CurrentCohortDistribution items={preview.current_cohorts} />
          <PolicyTargetSummary target={preview.target} />
          <PolicyView policy={preview.target.policy} />
        </>
      ) : null}
    </section>
  );
}

function ConfirmationStep({
  selection,
  preview,
  includeCustomProfiles,
  confirmed,
  onConfirmedChange,
  error,
}: {
  selection: PeopleSelection;
  preview: PolicyPreview;
  includeCustomProfiles: boolean;
  confirmed: boolean;
  onConfirmedChange(checked: boolean): void;
  error: Error | null;
}) {
  return (
    <section className="space-y-5">
      <div>
        <h2 className="text-lg font-semibold">Confirm policy job</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          The confirmation expires {formatDate(preview.confirmation_expires_at)}.
        </p>
      </div>
      <dl className="grid gap-3 sm:grid-cols-2">
        <Metric label="Selected people" value={selection.matched.toLocaleString()} />
        <Metric label="Operation" value={readableField(preview.target.kind)} />
        <Metric
          label="Inherited profiles moving"
          value={preview.inherited_profiles_will_move.toLocaleString()}
        />
        <Metric
          label="Custom profiles"
          value={includeCustomProfiles ? "Move with accounts" : "Remain unchanged"}
        />
      </dl>
      <CurrentCohortDistribution items={preview.current_cohorts} />
      <PolicyTargetSummary target={preview.target} />
      <label className="border-primary/30 bg-primary/5 flex items-start gap-3 rounded-lg border p-4 text-sm">
        <input
          type="checkbox"
          aria-label="I understand this creates a durable policy job"
          className="mt-0.5 size-4"
          checked={confirmed}
          onChange={(event) => onConfirmedChange(event.target.checked)}
        />
        <span>
          <span className="font-semibold">I understand this creates a durable policy job</span>
          <span className="text-muted-foreground mt-1 block">
            Eligible accounts move to an immutable managed cohort; stale accounts are safely
            skipped.
          </span>
        </span>
      </label>
      {error ? (
        <p className="text-destructive text-sm" role="alert">
          {safeErrorMessage(error)}
        </p>
      ) : null}
    </section>
  );
}

function JobView({
  job,
  error,
  cancelling,
  onCancel,
  onRetrySelection,
}: {
  job: PeopleBulkJob;
  error: Error | null;
  cancelling: boolean;
  onCancel(): void;
  onRetrySelection(): void;
}) {
  const pending = job.status === "queued" || job.status === "running";
  const percent = job.progress_total ? (job.progress_current / job.progress_total) * 100 : 0;
  const Icon = pending ? Clock3 : job.status === "completed" ? CheckCircle2 : AlertTriangle;
  return (
    <section className="space-y-5" aria-live="polite">
      <div className="flex items-start gap-3">
        <Icon className="text-muted-foreground mt-0.5 size-5" aria-hidden />
        <div>
          <h2 className="text-lg font-semibold">Policy job {job.status}</h2>
          <p className="text-muted-foreground mt-1 text-sm">Job {job.job_id}</p>
        </div>
      </div>
      <div className="space-y-2">
        <Progress value={percent} aria-label="Policy job progress" />
        <p className="text-muted-foreground text-sm">
          {job.progress_current.toLocaleString()} of {job.progress_total.toLocaleString()} processed
        </p>
      </div>
      <dl className="grid grid-cols-3 gap-3">
        <Metric label="Succeeded" value={job.succeeded.toLocaleString()} />
        <Metric label="Skipped" value={job.skipped.length.toLocaleString()} />
        <Metric label="Failed" value={job.failed.length.toLocaleString()} />
      </dl>
      {(job.skipped.length > 0 || job.failed.length > 0) && (
        <section>
          <h3 className="text-sm font-semibold">Account results</h3>
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {[...job.skipped, ...job.failed].map((result, index) => (
              <li key={`${result.account_id}:${result.reason}:${index}`}>
                Account {result.account_id} — {result.reason.replace(/_/g, " ")}
              </li>
            ))}
          </ul>
        </section>
      )}
      {error ? (
        <p className="text-destructive text-sm" role="alert">
          {safeErrorMessage(error)}
        </p>
      ) : null}
      <div className="flex flex-wrap gap-2">
        {pending ? (
          <Button type="button" variant="destructive" disabled={cancelling} onClick={onCancel}>
            Cancel policy job
          </Button>
        ) : null}
        {job.status === "failed" || job.failed.length > 0 || job.skipped.length > 0 ? (
          <Button type="button" variant="outline" onClick={onRetrySelection}>
            Refresh selection and retry
          </Button>
        ) : null}
      </div>
    </section>
  );
}

export function PolicyView({ policy }: { policy: EffectivePolicy }) {
  const values: Array<[string, string]> = [
    [
      "Libraries",
      policy.library_ids === null
        ? "All libraries"
        : policy.library_ids.length
          ? policy.library_ids.join(", ")
          : "None",
    ],
    ["Playback", policy.playback_allowed ? "Allowed" : "Not allowed"],
    ["Maximum streams", String(policy.max_streams)],
    ["Maximum profiles", String(policy.max_profiles)],
    ["Transcoding", policy.transcode_allowed ? "Allowed" : "Not allowed"],
    ["Maximum transcodes", String(policy.max_transcodes)],
    ["Downloads", policy.download_allowed ? "Allowed" : "Not allowed"],
    ["Transcoded downloads", policy.download_transcode_allowed ? "Allowed" : "Not allowed"],
    ["Maximum playback quality", policy.max_playback_quality || "Unrestricted"],
    [
      "Permissions",
      policy.allowed_permissions === null
        ? "Unrestricted"
        : policy.allowed_permissions.length
          ? policy.allowed_permissions.join(", ")
          : "None",
    ],
    ["Requests", policy.requests_allowed ? "Allowed" : "Not allowed"],
  ];
  return (
    <section
      className="border-border rounded-lg border p-4"
      aria-label="Authoritative target policy"
    >
      <div className="mb-3 flex items-center gap-2">
        <ShieldCheck className="size-4" aria-hidden />
        <h3 className="text-sm font-semibold">Authoritative target policy</h3>
      </div>
      <dl className="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-2">
        {values.map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="font-medium break-words">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function CurrentCohortDistribution({ items }: { items: PolicyPreview["current_cohorts"] }) {
  return (
    <section className="border-border rounded-lg border p-4" aria-label="Observed current cohorts">
      <h3 className="text-sm font-semibold">Observed current cohorts</h3>
      {items.length ? (
        <ul className="text-muted-foreground mt-2 space-y-2 text-sm">
          {items.map((item, index) => (
            <li key={`${item.cohort_id ?? item.group_id ?? "unmanaged"}:${index}`}>
              <span className="text-foreground font-medium">
                {item.source_template_key ??
                  item.cohort_id ??
                  `Access group ${item.group_id ?? "unknown"}`}
              </span>
              {item.source_template_revision
                ? ` · template revision ${item.source_template_revision}`
                : ""}
              {item.cohort_revision ? ` · cohort revision ${item.cohort_revision}` : ""}
              {` · ${item.count.toLocaleString()} ${item.state.replace(/_/g, " ")} accounts`}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-muted-foreground mt-2 text-sm">No current cohort observations.</p>
      )}
    </section>
  );
}

function PolicyTargetSummary({ target }: { target: PolicyPreview["target"] }) {
  const details: Array<[string, string]> = [
    ["Operation", readableField(target.kind)],
    ...(target.name ? ([["Target name", target.name]] as Array<[string, string]>) : []),
    ...(target.cohort_id ? [["Cohort ID", target.cohort_id] as [string, string]] : []),
    ...(target.cohort_revision
      ? [["Cohort revision", String(target.cohort_revision)] as [string, string]]
      : []),
    ...(target.parent_cohort_id
      ? [["Parent cohort", target.parent_cohort_id] as [string, string]]
      : []),
    ...(target.group_id ? [["Access group", String(target.group_id)] as [string, string]] : []),
    ...(target.template_key
      ? [
          [
            "Template",
            `${target.template_key}${target.template_revision ? ` · revision ${target.template_revision}` : ""}`,
          ] as [string, string],
        ]
      : []),
    ["Policy digest", target.policy_digest],
  ];
  return (
    <section
      className="border-primary/20 bg-primary/5 rounded-lg border p-4"
      aria-label="Authoritative policy target"
    >
      <h3 className="text-sm font-semibold">Authoritative policy target</h3>
      <dl className="mt-3 grid gap-x-4 gap-y-2 text-sm sm:grid-cols-2">
        {details.map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="font-medium break-all">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function OperationChoice({
  checked,
  label,
  description,
  onChange,
}: {
  checked: boolean;
  label: string;
  description: string;
  onChange(): void;
}) {
  return (
    <label className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 flex cursor-pointer items-start gap-3 rounded-lg border p-3">
      <input
        type="radio"
        name="policy-operation"
        aria-label={label}
        checked={checked}
        onChange={onChange}
        className="mt-1"
      />
      <span>
        <span className="block text-sm font-medium">{label}</span>
        <span className="text-muted-foreground mt-0.5 block text-xs">{description}</span>
      </span>
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
  onChange,
  options,
  disabled = false,
}: {
  label: string;
  value: string;
  onChange(value: string): void;
  options: Array<[string, string]>;
  disabled?: boolean;
}) {
  return (
    <Field label={label}>
      <select
        aria-label={label}
        disabled={disabled}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
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
  onChange,
  disabled = false,
}: {
  label: string;
  value: TriState;
  onChange(value: TriState): void;
  disabled?: boolean;
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
  onChange,
  disabled = false,
}: {
  label: string;
  value: string;
  onChange(value: string): void;
  disabled?: boolean;
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

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="border-border bg-muted/30 rounded-lg border p-3">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="mt-1 text-sm font-semibold break-words">{value}</dd>
    </div>
  );
}

function parseIntegerList(value: string): number[] | Error {
  const tokens = value.split(",").map((item) => item.trim());
  if (!tokens.length || tokens.some((item) => !/^\d+$/.test(item)))
    return new Error("Library operations require positive numeric library IDs.");
  const parsed = tokens.map(Number);
  if (parsed.some((item) => !Number.isSafeInteger(item) || item <= 0))
    return new Error("Library operations require positive numeric library IDs.");
  return [...new Set(parsed)].sort((left, right) => left - right);
}

function parseStringList(value: string): string[] | Error {
  const parsed = [
    ...new Set(
      value
        .split(",")
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ].sort();
  return parsed.length
    ? parsed
    : new Error("Permission operations require at least one permission.");
}

function buildPatch(draft: PatchDraft): PolicyPatch | Error {
  draft = normalizePatchDraft(draft);
  const patch: PolicyPatch = {};
  if (draft.librariesMode !== "unchanged") {
    if (["add", "remove", "replace"].includes(draft.librariesMode)) {
      const values = parseIntegerList(draft.libraryIDs);
      if (values instanceof Error) return values;
      patch.libraries = { mode: draft.librariesMode, values };
    } else patch.libraries = { mode: draft.librariesMode };
  }
  if (draft.permissionsMode !== "unchanged") {
    if (["add", "remove", "replace"].includes(draft.permissionsMode)) {
      const values = parseStringList(draft.permissions);
      if (values instanceof Error) return values;
      patch.permissions = { mode: draft.permissionsMode, values };
    } else patch.permissions = { mode: draft.permissionsMode };
  }
  const booleans: Array<[TriState, keyof PolicyPatch]> = [
    [draft.playback, "playback_allowed"],
    [draft.transcode, "transcode_allowed"],
    [draft.downloads, "download_allowed"],
    [draft.transcodedDownloads, "download_transcode_allowed"],
    [draft.requests, "requests_allowed"],
  ];
  booleans.forEach(([value, key]) => {
    if (value !== "unchanged") (patch[key] as boolean | undefined) = value === "true";
  });
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
  if (draft.maxPlaybackQuality.trim()) patch.max_playback_quality = draft.maxPlaybackQuality.trim();
  return patch;
}

function normalizePatchDraft(draft: PatchDraft): PatchDraft {
  const normalized = { ...draft };
  if (normalized.playback === "false") {
    normalized.maxStreams = "0";
    normalized.transcode = "false";
    normalized.maxTranscodes = "0";
    normalized.downloads = "false";
    normalized.transcodedDownloads = "false";
    normalized.maxPlaybackQuality = "";
    return normalized;
  }
  if (normalized.transcode === "false") normalized.maxTranscodes = "0";
  if (normalized.downloads === "false") normalized.transcodedDownloads = "false";
  return normalized;
}

function describePatchDraft(draft: PatchDraft): string[] {
  const changes: string[] = [];
  if (draft.librariesMode !== "unchanged")
    changes.push(
      `Libraries: ${draft.librariesMode}${draft.libraryIDs.trim() ? ` ${draft.libraryIDs.trim()}` : ""}`,
    );
  if (draft.permissionsMode !== "unchanged")
    changes.push(
      `Permissions: ${draft.permissionsMode}${draft.permissions.trim() ? ` ${draft.permissions.trim()}` : ""}`,
    );
  const booleans: Array<[string, TriState]> = [
    ["Playback", draft.playback],
    ["Transcoding", draft.transcode],
    ["Downloads", draft.downloads],
    ["Transcoded downloads", draft.transcodedDownloads],
    ["Requests", draft.requests],
  ];
  booleans.forEach(([label, value]) => {
    if (value !== "unchanged")
      changes.push(`${label}: ${value === "true" ? "enabled" : "disabled"}`);
  });
  const values: Array<[string, string]> = [
    ["Maximum streams", draft.maxStreams],
    ["Maximum profiles", draft.maxProfiles],
    ["Maximum transcodes", draft.maxTranscodes],
    ["Maximum playback quality", draft.maxPlaybackQuality],
  ];
  values.forEach(([label, value]) => {
    if (value.trim()) changes.push(`${label}: ${value.trim()}`);
  });
  return changes;
}

function readableField(field: string): string {
  const value = field.replace(/_/g, " ");
  return value === "max streams" ? "maximum streams" : value;
}

function safeErrorMessage(error: unknown): string {
  if (error instanceof AdminV2ClientError) {
    if (error.code === "selection_expired")
      return "The immutable selection expired. Refresh it before retrying.";
    if (error.code === "policy_confirmation_stale")
      return "The policy preview changed or expired. Refresh the selection and preview again.";
    if (error.code === "job_not_cancellable")
      return "This policy job has already reached a state that cannot be cancelled.";
    if (error.code === "idempotency_conflict")
      return "A different command already used this submission key. Review and submit again.";
    if (error.code === "validation_failed")
      return "The policy operation is not valid. Review the highlighted fields.";
    if (error.code === "entitlements_unavailable")
      return "Entitlement policy service is temporarily unavailable. Try again.";
  }
  return "The policy request could not be completed. Try again.";
}

function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function")
    return crypto.randomUUID();
  return `policy-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}
