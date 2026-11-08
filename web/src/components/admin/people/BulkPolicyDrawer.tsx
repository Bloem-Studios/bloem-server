import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import type { AdminContextKey } from "@/api/bloemTypes";
import { PolicyChangeForm } from "@/components/admin/policy/PolicyChangeForm";
import {
  buildPolicyCommand,
  emptyPolicyChangeDraft,
  type PolicyChangeDraft,
} from "@/components/admin/policy/policyDraft";
import {
  ConfirmChange,
  Count,
  CustomProfilesOption,
  PolicyJobView,
  PolicyTarget,
  PreviewImpact,
  StepList,
} from "@/components/admin/policy/PolicyReview";
import {
  formatDateTime,
  needsFreshSelection,
  policyErrorMessage,
  type NamedLibrary,
} from "@/components/admin/policy/policyVocabulary";
import {
  useCancelPolicyJob,
  useCreatePolicyJob,
  useCreatePolicyPreview,
  usePolicyJob,
  type EntitlementCohort,
  type PolicyPreview,
} from "@/hooks/queries/admin/entitlementCohorts";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import type { PeopleBulkJob, PeopleSelection } from "@/hooks/queries/admin/organizationPeople";

const STEPS = ["Accounts", "Change", "Review", "Confirm"];

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
  const [draft, setDraft] = useState<PolicyChangeDraft>(() =>
    emptyPolicyChangeDraft("assign", initialCohortID ?? cohorts[0]?.cohort_id ?? ""),
  );
  const [includeCustomProfiles, setIncludeCustomProfiles] = useState(false);
  const [preview, setPreview] = useState<PolicyPreview>();
  const [confirmed, setConfirmed] = useState(false);
  const [submittedJob, setSubmittedJob] = useState<PeopleBulkJob>();
  const [validationError, setValidationError] = useState("");
  const previewGuard = useRef(false);
  const submitGuard = useRef(false);
  const cancelGuard = useRef(false);
  const idempotencyKey = useRef("");
  const handledInitialCohortID = useRef<string | undefined>(undefined);
  const createPreview = useCreatePolicyPreview(contextKey);
  const createJob = useCreatePolicyJob(contextKey);
  const polledJob = usePolicyJob(contextKey, submittedJob?.job_id);
  const cancelJob = useCancelPolicyJob(contextKey);
  const libraryQuery = useOrganizationLibraries(contextKey, open);
  const libraries = useMemo<NamedLibrary[]>(
    () =>
      (libraryQuery.data ?? []).map((library) => ({ id: library.folder_id, name: library.name })),
    [libraryQuery.data],
  );
  const visibleJob = polledJob.data ?? submittedJob;
  const cohortID = draft.cohortID;

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
    const setCohort = (value: string) => setDraft((current) => ({ ...current, cohortID: value }));
    if (initialCohortID && handledInitialCohortID.current !== initialCohortID) {
      if (cohorts.length === 0) return;
      handledInitialCohortID.current = initialCohortID;
      setCohort(
        cohorts.some((cohort) => cohort.cohort_id === initialCohortID) ? initialCohortID : "",
      );
      return;
    }
    if (!initialCohortID) {
      handledInitialCohortID.current = undefined;
      if (!cohortID && cohorts[0]) {
        setCohort(cohorts[0].cohort_id);
        return;
      }
    }
    if (cohortID && !cohorts.some((cohort) => cohort.cohort_id === cohortID)) setCohort("");
  }, [cohortID, cohorts, initialCohortID]);

  function clearPreview() {
    setPreview(undefined);
    setConfirmed(false);
    setValidationError("");
    createPreview.reset();
    createJob.reset();
    idempotencyKey.current = "";
  }

  function command() {
    const result = buildPolicyCommand(draft, cohorts, includeCustomProfiles);
    if (result instanceof Error) {
      setValidationError(result.message);
      return undefined;
    }
    setValidationError("");
    return result;
  }

  async function previewImpact() {
    if (previewGuard.current) return;
    const next = command();
    if (!next) return;
    previewGuard.current = true;
    try {
      setPreview(
        await createPreview.mutateAsync({ selectionToken: selection.token, command: next }),
      );
      setStep(3);
    } catch {
      // The mutation exposes only the safe API error rendered by this drawer.
    } finally {
      previewGuard.current = false;
    }
  }

  async function submit() {
    if (submitGuard.current || !preview || !confirmed) return;
    const next = command();
    if (!next) return;
    submitGuard.current = true;
    if (!idempotencyKey.current) idempotencyKey.current = newIdempotencyKey();
    try {
      setSubmittedJob(
        await createJob.mutateAsync({
          selectionToken: selection.token,
          confirmationToken: preview.confirmation_token,
          idempotencyKey: idempotencyKey.current,
          command: next,
        }),
      );
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
      setSubmittedJob(await cancelJob.mutateAsync(visibleJob.job_id));
    } catch {
      // The job view renders a bounded safe error and keeps polling status.
    } finally {
      cancelGuard.current = false;
    }
  }

  const requestError = createPreview.error ?? createJob.error ?? polledJob.error ?? cancelJob.error;
  const stepError = step === 3 ? createPreview.error : step === 4 ? createJob.error : requestError;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full gap-0 overflow-hidden sm:max-w-2xl">
        <SheetHeader className="border-border space-y-3 border-b px-5 py-4 pr-12">
          <SheetTitle>Change plan or settings</SheetTitle>
          <SheetDescription>
            For {selection.matched.toLocaleString()}{" "}
            {selection.matched === 1 ? "account" : "accounts"} in {organizationName}
          </SheetDescription>
          {!visibleJob ? <StepList steps={STEPS} current={step} /> : null}
        </SheetHeader>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-5">
          {visibleJob ? (
            <PolicyJobView
              job={visibleJob}
              error={polledJob.error ?? cancelJob.error}
              cancelling={cancelJob.isPending}
              onCancel={() => void cancel()}
              onStartOver={onRetrySelection}
            />
          ) : step === 1 ? (
            <section className="space-y-4">
              <h2 className="text-lg font-medium">Check the accounts</h2>
              <dl className="grid grid-cols-2 gap-3">
                <Count label="Selected" value={selection.matched} strong />
                <Count label="Left out by your filters" value={selection.excluded} />
              </dl>
              <p className="text-muted-foreground text-sm">
                This list is fixed now, so people who join or match your filters later are not
                included. Finish by {formatDateTime(selection.expires_at)}.
              </p>
            </section>
          ) : step === 2 ? (
            <section className="space-y-4">
              <h2 className="text-lg font-medium">What should change?</h2>
              <PolicyChangeForm
                draft={draft}
                onChange={(next) => {
                  setDraft(next);
                  clearPreview();
                }}
                cohorts={cohorts}
                libraries={libraries}
                error={validationError}
                radioName="people-policy"
              />
              <CustomProfilesOption
                checked={includeCustomProfiles}
                onCheckedChange={(checked) => {
                  setIncludeCustomProfiles(checked);
                  clearPreview();
                }}
              />
            </section>
          ) : step === 3 ? (
            <section className="space-y-5">
              <h2 className="text-lg font-medium">Review the change</h2>
              {preview ? (
                <>
                  <PreviewImpact preview={preview} />
                  <PolicyTarget target={preview.target} libraries={libraries} />
                </>
              ) : !createPreview.error ? (
                <p
                  className="border-border bg-muted/40 rounded-lg border p-3 text-sm"
                  role="status"
                >
                  You changed an option, so this preview is out of date. Update it to continue.
                </p>
              ) : null}
            </section>
          ) : preview ? (
            <section className="space-y-5">
              <h2 className="text-lg font-medium">Confirm</h2>
              <PolicyTarget target={preview.target} libraries={libraries} />
              <ConfirmChange
                accounts={selection.matched}
                expiresAt={formatDateTime(preview.confirmation_expires_at)}
                checked={confirmed}
                onCheckedChange={setConfirmed}
              />
            </section>
          ) : null}

          {stepError && !visibleJob ? (
            <div
              className="border-destructive/30 bg-destructive/10 rounded-lg border p-3"
              role="alert"
            >
              <p className="text-destructive text-sm">{policyErrorMessage(stepError)}</p>
              {needsFreshSelection(stepError) ? (
                <Button className="mt-3" size="sm" variant="outline" onClick={onRetrySelection}>
                  Review the accounts again
                </Button>
              ) : null}
            </div>
          ) : null}
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
                Next
              </Button>
            ) : step === 2 || (step === 3 && !preview) ? (
              <Button
                type="button"
                disabled={createPreview.isPending}
                onClick={() => void previewImpact()}
              >
                {createPreview.isPending ? "Checking…" : step === 2 ? "Preview" : "Update preview"}
              </Button>
            ) : step === 3 ? (
              <Button type="button" onClick={() => setStep(4)}>
                Next
              </Button>
            ) : (
              <Button
                type="button"
                disabled={!confirmed || createJob.isPending}
                onClick={() => void submit()}
              >
                Apply to {selection.matched.toLocaleString()}{" "}
                {selection.matched === 1 ? "account" : "accounts"}
              </Button>
            )}
          </SheetFooter>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function")
    return crypto.randomUUID();
  return `policy-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
