// Bloem: review, confirm and progress views shared by the bulk policy flows.
import { useBloemT } from "@/bloem/i18n";
import { AlertTriangle, CheckCircle2, Clock3 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import type { PolicyPreview } from "@/hooks/queries/admin/entitlementCohorts";
import type { PeopleBulkJob } from "@/hooks/queries/admin/organizationPeople";
import {
  changeKindLabel,
  jobHeading,
  policyErrorMessage,
  policyFieldLabel,
  policyRows,
  policyStateLabel,
  resultReasonLabel,
  type NamedLibrary,
  type PolicyValues,
} from "./policyVocabulary";

/** Numbered steps for a multi-step flow. */
export function StepList({ steps, current }: { steps: string[]; current: number }) {
  const bt = useBloemT();
  return (
    <ol
      aria-label={bt("bloem.admin.policy.review.steps")}
      className="flex flex-wrap gap-x-5 gap-y-2 text-sm"
    >
      {steps.map((label, index) => {
        const number = index + 1;
        const state = number < current ? "done" : number === current ? "current" : "next";
        return (
          <li
            key={label}
            aria-current={state === "current" ? "step" : undefined}
            className={
              state === "next"
                ? "text-muted-foreground flex items-center gap-2"
                : "flex items-center gap-2"
            }
          >
            <span
              className={
                state === "current"
                  ? "bg-primary text-primary-foreground inline-grid size-6 place-items-center rounded-full text-xs font-medium tabular-nums"
                  : "bg-muted inline-grid size-6 place-items-center rounded-full text-xs tabular-nums"
              }
            >
              {state === "done" ? (
                <CheckCircle2
                  className="size-3.5"
                  aria-label={bt("bloem.admin.policy.review.done")}
                />
              ) : (
                number
              )}
            </span>
            <span className={state === "current" ? "font-medium" : undefined}>{label}</span>
          </li>
        );
      })}
    </ol>
  );
}

/** Every setting in a policy, in plain words. */
export function PolicySummary({
  policy,
  libraries,
  label,
  compact = false,
}: {
  policy: PolicyValues;
  libraries?: NamedLibrary[];
  label: string;
  compact?: boolean;
}) {
  return (
    <section aria-label={label} className={compact ? "" : "border-border rounded-lg border p-4"}>
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
        {policyRows(policy, libraries).map(([name, value]) => (
          <div key={name} className="min-w-0">
            <dt className="text-muted-foreground">{name}</dt>
            <dd className="font-medium break-words">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

/** What the change gives the accounts, with IDs tucked under Details. */
export function PolicyTarget({
  target,
  libraries,
}: {
  target: PolicyPreview["target"];
  libraries?: NamedLibrary[];
}) {
  const bt = useBloemT();
  const headline = target.template_key
    ? `Plan ${target.template_key}${target.template_revision ? `, revision ${target.template_revision}` : ""}`
    : target.name
      ? `Cohort ${target.name}${target.cohort_revision ? `, revision ${target.cohort_revision}` : ""}`
      : changeKindLabel(target.kind);
  const details: Array<[string, string]> = [
    ["Change", changeKindLabel(target.kind)],
    ...(target.cohort_id ? [["Cohort ID", target.cohort_id] as [string, string]] : []),
    ...(target.parent_cohort_id
      ? [["Started from cohort", target.parent_cohort_id] as [string, string]]
      : []),
    ...(target.group_id ? [["Access group", String(target.group_id)] as [string, string]] : []),
    ["Settings fingerprint", target.policy_digest],
  ];
  return (
    <section className="space-y-3" aria-label={bt("bloem.admin.policy.review.whatTheyLlGet")}>
      <div>
        <h3 className="text-sm font-medium">{bt("bloem.admin.policy.review.whatTheyLlGet2")}</h3>
        <p className="text-muted-foreground text-sm">{headline}</p>
      </div>
      <PolicySummary
        policy={target.policy}
        libraries={libraries}
        label={bt("bloem.admin.policy.review.newSettings")}
      />
      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          {bt("bloem.admin.policy.review.details")}
        </summary>
        <dl className="mt-2 grid gap-x-6 gap-y-2 sm:grid-cols-2">
          {details.map(([name, value]) => (
            <div key={name} className="min-w-0">
              <dt className="text-muted-foreground">{name}</dt>
              <dd className="font-mono text-xs break-all">{value}</dd>
            </div>
          ))}
        </dl>
      </details>
    </section>
  );
}

/** The preview counts, the settings that change and where accounts are now. */
export function PreviewImpact({ preview }: { preview: PolicyPreview }) {
  const bt = useBloemT();
  return (
    <div className="space-y-5">
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Count label={bt("bloem.admin.policy.review.selected")} value={preview.matched} strong />
        <Count
          label={bt("bloem.admin.policy.review.alreadySetUpThisWay")}
          value={preview.already_compliant}
        />
        <Count
          label={bt("bloem.admin.policy.review.canTBeChanged")}
          value={preview.ineligible_or_stale}
        />
        <Count label={bt("bloem.admin.policy.review.leftOut")} value={preview.excluded} />
      </dl>

      <section aria-label={bt("bloem.admin.policy.review.settingsThatChange")}>
        <h3 className="text-sm font-medium">
          {bt("bloem.admin.policy.review.settingsThatChange")}
        </h3>
        {preview.diff.length ? (
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {preview.diff.map((diff) => (
              <li key={diff.field}>
                {bt("bloem.admin.policy.review.fieldForAccounts", {
                  field: capitalize(policyFieldLabel(diff.field)),
                  count: diff.changed_accounts,
                })}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground mt-2 text-sm">
            {bt("bloem.admin.policy.review.noSettingsChange")}
          </p>
        )}
      </section>

      <section aria-label={bt("bloem.admin.policy.review.profiles")}>
        <h3 className="text-sm font-medium">{bt("bloem.admin.policy.review.profiles")}</h3>
        <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
          <li>
            {bt("bloem.admin.policy.review.thatFollowTheirAccountChange", {
              inheritedProfilesWillMove: preview.inherited_profiles_will_move.toLocaleString(),
            })}
          </li>
          <li>
            {preview.custom_profiles_will_move
              ? bt("bloem.admin.policy.review.withTheirOwnSettingsChange", {
                  customProfilesWillMove: preview.custom_profiles_will_move.toLocaleString(),
                })
              : bt("bloem.admin.policy.review.withTheirOwnSettingsStay", {
                  customProfilesWillRemain: preview.custom_profiles_will_remain.toLocaleString(),
                })}
          </li>
        </ul>
      </section>

      {preview.current_cohorts.length ? (
        <section aria-label={bt("bloem.admin.policy.review.whereTheyAreNow")}>
          <h3 className="text-sm font-medium">{bt("bloem.admin.policy.review.whereTheyAreNow")}</h3>
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {preview.current_cohorts.map((item, index) => (
              <li key={`${item.cohort_id ?? item.group_id ?? "none"}:${index}`}>
                {bt("bloem.admin.policy.review.cohortLine", {
                  count: item.count,
                  where: item.source_template_key
                    ? item.source_template_revision
                      ? bt("bloem.admin.policy.review.planRevision", {
                          plan: item.source_template_key,
                          revision: item.source_template_revision,
                        })
                      : bt("bloem.admin.policy.review.plan", { plan: item.source_template_key })
                    : item.group_name
                      ? item.group_name
                      : policyStateLabel(item.state).toLowerCase(),
                })}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

/** Whether profiles with their own settings change along with their account. */
export function CustomProfilesOption({
  checked,
  onCheckedChange,
}: {
  checked: boolean;
  onCheckedChange(value: boolean): void;
}) {
  const bt = useBloemT();
  return (
    <label className="border-border flex items-start gap-3 rounded-lg border p-3 text-sm">
      <input
        type="checkbox"
        className="mt-0.5 size-4"
        checked={checked}
        onChange={(event) => onCheckedChange(event.target.checked)}
      />
      <span>
        <span className="font-medium">
          {bt("bloem.admin.policy.review.alsoChangeProfilesWithTheir")}
        </span>
        <span className="text-muted-foreground mt-0.5 block">
          {bt("bloem.admin.policy.review.offByDefaultProfilesThat")}
        </span>
      </span>
    </label>
  );
}

/** The final tick box before a bulk change starts. */
export function ConfirmChange({
  accounts,
  expiresAt,
  checked,
  onCheckedChange,
}: {
  accounts: number;
  expiresAt: string;
  checked: boolean;
  onCheckedChange(value: boolean): void;
}) {
  const bt = useBloemT();
  return (
    <label className="border-border bg-muted/30 flex items-start gap-3 rounded-lg border p-4 text-sm">
      <input
        type="checkbox"
        className="mt-0.5 size-4"
        checked={checked}
        onChange={(event) => onCheckedChange(event.target.checked)}
      />
      <span>
        <span className="font-medium">
          {bt("bloem.admin.policy.review.checkedForAccounts", { count: accounts })}
        </span>
        <span className="text-muted-foreground mt-0.5 block">
          {bt("bloem.admin.policy.review.confirmByAccountsThatChange", { expiresAt })}
        </span>
      </span>
    </label>
  );
}

/** Progress and results of a running or finished bulk change. */
export function PolicyJobView({
  job,
  error,
  cancelling,
  accountName = (id) => `Account ${id}`,
  onCancel,
  onStartOver,
}: {
  job: PeopleBulkJob;
  error: unknown;
  cancelling: boolean;
  accountName?: (id: number) => string;
  onCancel(): void;
  onStartOver(): void;
}) {
  const bt = useBloemT();
  const pending = job.status === "queued" || job.status === "running";
  const percent = job.progress_total ? (job.progress_current / job.progress_total) * 100 : 0;
  const Icon = pending ? Clock3 : job.status === "completed" ? CheckCircle2 : AlertTriangle;
  const problems = [...job.skipped, ...job.failed];
  return (
    <section className="space-y-5" aria-live="polite">
      <div className="flex items-start gap-3">
        <Icon className="text-muted-foreground mt-1 size-5" aria-hidden />
        <div>
          <h2 className="text-lg font-medium">{jobHeading(job)}</h2>
          <p className="text-muted-foreground text-sm tabular-nums">
            {bt("bloem.admin.policy.review.ofAccountsDone", {
              progressCurrent: job.progress_current.toLocaleString(),
              progressTotal: job.progress_total.toLocaleString(),
            })}
          </p>
        </div>
      </div>
      <Progress value={percent} aria-label={bt("bloem.admin.policy.review.progress")} />
      <dl className="grid grid-cols-3 gap-3">
        <Count label={bt("bloem.admin.policy.review.changed")} value={job.succeeded} />
        <Count label={bt("bloem.admin.policy.review.skipped")} value={job.skipped.length} />
        <Count label={bt("bloem.admin.policy.review.failed")} value={job.failed.length} />
      </dl>
      {problems.length ? (
        <section aria-label={bt("bloem.admin.policy.review.accountsNotChanged")}>
          <h3 className="text-sm font-medium">
            {bt("bloem.admin.policy.review.accountsNotChanged")}
          </h3>
          <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
            {problems.map((result, index) => (
              <li key={`${result.account_id}:${result.reason}:${index}`}>
                {bt("bloem.admin.policy.review.accountResultReason", {
                  account: accountName(result.account_id),
                  resultReason: resultReasonLabel(result.reason),
                })}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {error ? (
        <p className="text-destructive text-sm" role="alert">
          {policyErrorMessage(error)}
        </p>
      ) : null}
      <div className="flex flex-wrap gap-2">
        {pending ? (
          <Button type="button" variant="outline" disabled={cancelling} onClick={onCancel}>
            {bt("bloem.admin.policy.review.stop")}
          </Button>
        ) : null}
        {!pending || problems.length ? (
          <Button type="button" variant="outline" onClick={onStartOver}>
            {bt("bloem.admin.policy.review.reviewTheseAccountsAgain")}
          </Button>
        ) : null}
      </div>
    </section>
  );
}

export function Count({
  label,
  value,
  strong = false,
}: {
  label: string;
  value: number | string;
  strong?: boolean;
}) {
  return (
    <div className="border-border rounded-lg border p-3">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className={`mt-1 tabular-nums ${strong ? "text-lg font-medium" : "font-medium"}`}>
        {typeof value === "number" ? value.toLocaleString() : value}
      </dd>
    </div>
  );
}

function capitalize(value: string): string {
  return value ? value[0]!.toUpperCase() + value.slice(1) : value;
}
