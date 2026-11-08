import { useMemo } from "react";
import { GitBranch } from "lucide-react";
import { Link } from "react-router";

import { PolicySummary } from "@/components/admin/policy/PolicyReview";
import {
  derivationLabel,
  formatDateTime,
  type NamedLibrary,
} from "@/components/admin/policy/policyVocabulary";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import {
  useOrganizationEntitlementCohorts,
  type EntitlementCohort,
} from "@/hooks/queries/admin/entitlementCohorts";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function EntitlementCohortsPage() {
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const cohorts = useOrganizationEntitlementCohorts(contextKey, true);
  const libraryQuery = useOrganizationLibraries(contextKey, Boolean(active));
  const libraries = useMemo<NamedLibrary[]>(
    () =>
      (libraryQuery.data ?? []).map((library) => ({ id: library.folder_id, name: library.name })),
    [libraryQuery.data],
  );
  useDocumentTitle("Cohorts");
  const activeCohorts = cohorts.data?.filter((cohort) => !cohort.archived) ?? [];
  const archivedCohorts = cohorts.data?.filter((cohort) => cohort.archived) ?? [];

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Cohorts
          </h1>
          <p className="page-subtitle max-w-3xl">
            A cohort is a group of accounts in {active?.name ?? "this organization"} that share the
            same settings. A cohort never changes. To give accounts different settings, select them
            on the People page and move them.
          </p>
        </div>
      </div>

      {cohorts.isLoading ? (
        <div className="grid gap-4 xl:grid-cols-2" role="status" aria-label="Loading cohorts">
          <Skeleton className="h-80" />
          <Skeleton className="h-80" />
        </div>
      ) : cohorts.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          Cohorts couldn’t be loaded. Reload the page to try again.
        </div>
      ) : cohorts.data?.length === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center text-sm">
          <GitBranch className="mx-auto mb-3 size-8" aria-hidden />
          <p>No cohorts yet. One appears when you first put people on a plan.</p>
          <Button asChild size="sm" variant="outline" className="mt-4">
            <Link to="/admin/organization/people">Go to People</Link>
          </Button>
        </div>
      ) : (
        <>
          <div className="grid items-start gap-5 xl:grid-cols-2">
            {activeCohorts.map((cohort) => (
              <CohortCard key={cohort.cohort_id} cohort={cohort} libraries={libraries} />
            ))}
          </div>
          {archivedCohorts.length ? (
            <details className="space-y-4">
              <summary className="text-muted-foreground hover:text-foreground cursor-pointer text-sm transition-colors">
                Archived cohorts ({archivedCohorts.length})
              </summary>
              <p className="text-muted-foreground mt-3 text-sm">
                Kept for the record. Nobody can be moved to an archived cohort.
              </p>
              <div className="mt-4 grid items-start gap-5 xl:grid-cols-2">
                {archivedCohorts.map((cohort) => (
                  <CohortCard key={cohort.cohort_id} cohort={cohort} libraries={libraries} />
                ))}
              </div>
            </details>
          ) : null}
        </>
      )}
    </section>
  );
}

function CohortCard({
  cohort,
  libraries,
}: {
  cohort: EntitlementCohort;
  libraries: NamedLibrary[];
}) {
  const origin = cohort.source_template_key
    ? `From plan ${cohort.source_template_key}${cohort.source_template_revision ? `, revision ${cohort.source_template_revision}` : ""}`
    : derivationLabel(cohort.derivation_kind);
  const details: Array<[string, string]> = [
    ["How it was made", derivationLabel(cohort.derivation_kind)],
    ["Cohort revision", String(cohort.revision)],
    ...(cohort.parent_cohort_id
      ? [["Started from cohort", cohort.parent_cohort_id] as [string, string]]
      : []),
    ["Created", formatDateTime(cohort.created_at)],
    ...(cohort.created_by_account_id
      ? [["Created by", `Account ${cohort.created_by_account_id}`] as [string, string]]
      : []),
    ["Access group", String(cohort.access_group_id)],
    ["Cohort ID", cohort.cohort_id],
    ["Settings fingerprint", cohort.policy_digest],
  ];
  return (
    <article
      className="surface-panel space-y-4 rounded-2xl p-5"
      aria-label={`${cohort.name} revision ${cohort.revision}`}
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-lg font-medium">{cohort.name}</h2>
          <p className="text-muted-foreground mt-1 text-sm">{origin}</p>
        </div>
        <p className="text-muted-foreground text-sm tabular-nums">
          {cohort.member_count.toLocaleString()}{" "}
          {cohort.member_count === 1 ? "account" : "accounts"}
          {cohort.archived ? " · Archived" : null}
        </p>
      </header>

      <PolicySummary policy={cohort.policy} libraries={libraries} label="Settings" />

      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          Details
        </summary>
        <dl className="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-2">
          {details.map(([label, value]) => (
            <div key={label} className="min-w-0">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="break-all">{value}</dd>
            </div>
          ))}
        </dl>
      </details>

      {!cohort.archived ? (
        <div className="flex justify-end">
          <Button asChild size="sm" variant="outline">
            <Link
              to={`/admin/organization/people?policy_cohort=${encodeURIComponent(cohort.cohort_id)}`}
              aria-label={`Move people to ${cohort.name}`}
            >
              Move people here
            </Link>
          </Button>
        </div>
      ) : null}
    </article>
  );
}
