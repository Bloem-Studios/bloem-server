import { Archive, GitBranch, ShieldCheck, Users } from "lucide-react";
import { Link } from "react-router";

import { AdminV2ClientError } from "@/api/adminV2Client";
import { PolicyView } from "@/components/admin/people/BulkPolicyDrawer";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import {
  useOrganizationEntitlementCohorts,
  type EntitlementCohort,
} from "@/hooks/queries/admin/entitlementCohorts";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function EntitlementCohortsPage() {
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const cohorts = useOrganizationEntitlementCohorts(contextKey, true);
  useDocumentTitle("Policy Cohorts");

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Policy Cohorts
          </h1>
          <p className="page-subtitle">
            Immutable entitlement policy history for {active?.name ?? "this organization"}.
          </p>
        </div>
      </div>

      <div className="border-border bg-muted/30 flex items-start gap-3 rounded-xl border p-4 text-sm">
        <ShieldCheck className="text-muted-foreground mt-0.5 size-5 shrink-0" aria-hidden />
        <p>
          Cohort policy is read-only. Applying a template or changing policy for selected people
          resolves or creates a new immutable revision; it never edits this history in place.
        </p>
      </div>

      {cohorts.isLoading ? (
        <div
          className="grid gap-4 xl:grid-cols-2"
          role="status"
          aria-label="Loading policy cohorts"
        >
          <Skeleton className="h-96" />
          <Skeleton className="h-96" />
        </div>
      ) : cohorts.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          {cohorts.error instanceof AdminV2ClientError
            ? cohorts.error.message
            : "Policy cohorts could not be loaded."}
        </div>
      ) : cohorts.data?.length === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center text-sm">
          <GitBranch className="mx-auto mb-3 size-8" aria-hidden />
          No policy cohorts have been materialized for this organization yet.
        </div>
      ) : (
        <div className="grid items-start gap-5 xl:grid-cols-2">
          {cohorts.data?.map((cohort) => (
            <CohortCard key={cohort.cohort_id} cohort={cohort} />
          ))}
        </div>
      )}
    </section>
  );
}

function CohortCard({ cohort }: { cohort: EntitlementCohort }) {
  return (
    <article
      className={
        cohort.archived
          ? "surface-panel rounded-2xl p-5 opacity-75"
          : "surface-panel rounded-2xl p-5"
      }
      aria-label={`${cohort.name} revision ${cohort.revision}`}
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">{cohort.name}</h2>
          <p className="text-muted-foreground mt-1 text-xs">
            Cohort revision {cohort.revision} · access group {cohort.access_group_id}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Badge variant="secondary">
            <Users className="size-3" aria-hidden />
            {cohort.member_count.toLocaleString()} people
          </Badge>
          {cohort.archived ? (
            <Badge variant="outline">
              <Archive className="size-3" aria-hidden /> Archived
            </Badge>
          ) : (
            <Badge variant="secondary">Active</Badge>
          )}
        </div>
      </header>

      <dl className="border-border my-5 grid gap-3 border-y py-4 text-sm sm:grid-cols-2">
        <Detail
          label="Source template"
          value={
            cohort.source_template_key
              ? `${cohort.source_template_key} · revision ${cohort.source_template_revision ?? "unknown"}`
              : "Managed default"
          }
        />
        <Detail label="Derivation" value={cohort.derivation_kind.replace(/_/g, " ")} />
        <Detail
          label="Lineage"
          value={
            cohort.parent_cohort_id
              ? `Derived from ${cohort.parent_cohort_id}`
              : "Root cohort revision"
          }
        />
        <Detail
          label="Created"
          value={`${formatTimestamp(cohort.created_at)} · ${
            cohort.created_by_account_id
              ? `Created by account ${cohort.created_by_account_id}`
              : "Creation actor unavailable"
          }`}
        />
        <Detail label="Policy digest" value={cohort.policy_digest} full />
      </dl>

      <section aria-label="Effective policy">
        <PolicyView policy={cohort.policy} />
      </section>

      <div className="mt-5 flex justify-end">
        <Button asChild size="sm">
          <Link
            to={`/admin/organization/people?policy_cohort=${encodeURIComponent(cohort.cohort_id)}`}
            aria-label={`Apply ${cohort.name} to people`}
          >
            Apply to people
          </Link>
        </Button>
      </div>
    </article>
  );
}

function Detail({ label, value, full = false }: { label: string; value: string; full?: boolean }) {
  return (
    <div className={full ? "sm:col-span-2" : undefined}>
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="mt-1 font-medium break-all capitalize">{value}</dd>
    </div>
  );
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Timestamp unavailable";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}
