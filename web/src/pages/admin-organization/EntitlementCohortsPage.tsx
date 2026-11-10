import { useMemo } from "react";
import { GitBranch } from "lucide-react";
import { Link } from "react-router";

import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const cohorts = useOrganizationEntitlementCohorts(contextKey, true);
  const libraryQuery = useOrganizationLibraries(contextKey, Boolean(active));
  const libraries = useMemo<NamedLibrary[]>(
    () =>
      (libraryQuery.data ?? []).map((library) => ({ id: library.folder_id, name: library.name })),
    [libraryQuery.data],
  );
  useDocumentTitle(bt("bloem.admin.organization.cohorts.title"));
  const activeCohorts = cohorts.data?.filter((cohort) => !cohort.archived) ?? [];
  const archivedCohorts = cohorts.data?.filter((cohort) => cohort.archived) ?? [];

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.organization.cohorts.title")}
          </h1>
          <p className="page-subtitle max-w-3xl">
            {bt("bloem.admin.organization.cohorts.subtitle", {
              organization: active?.name ?? bt("bloem.admin.organization.thisOrganization"),
            })}
          </p>
        </div>
      </div>

      {cohorts.isLoading ? (
        <div
          className="grid gap-4 xl:grid-cols-2"
          role="status"
          aria-label={bt("bloem.admin.organization.cohorts.loading")}
        >
          <Skeleton className="h-80" />
          <Skeleton className="h-80" />
        </div>
      ) : cohorts.isError ? (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          {bt("bloem.admin.organization.cohorts.loadFailed")}
        </div>
      ) : cohorts.data?.length === 0 ? (
        <div className="surface-panel text-muted-foreground rounded-2xl p-10 text-center text-sm">
          <GitBranch className="mx-auto mb-3 size-8" aria-hidden />
          <p>{bt("bloem.admin.organization.cohorts.empty")}</p>
          <Button asChild size="sm" variant="outline" className="mt-4">
            <Link to="/admin/organization/people">
              {bt("bloem.admin.organization.cohorts.goToPeople")}
            </Link>
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
                {bt("bloem.admin.organization.cohorts.archived", {
                  count: archivedCohorts.length,
                })}
              </summary>
              <p className="text-muted-foreground mt-3 text-sm">
                {bt("bloem.admin.organization.cohorts.archivedNote")}
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
  const bt = useBloemT();
  const origin = cohort.source_template_key
    ? cohort.source_template_revision
      ? bt("bloem.admin.organization.cohorts.fromPlanRevision", {
          plan: cohort.source_template_key,
          revision: cohort.source_template_revision,
        })
      : bt("bloem.admin.organization.cohorts.fromPlan", { plan: cohort.source_template_key })
    : derivationLabel(cohort.derivation_kind);
  const details: Array<[string, string]> = [
    [bt("bloem.admin.organization.cohorts.howItWasMade"), derivationLabel(cohort.derivation_kind)],
    [bt("bloem.admin.organization.cohorts.cohortRevision"), String(cohort.revision)],
    ...(cohort.parent_cohort_id
      ? [
          [bt("bloem.admin.organization.cohorts.startedFromCohort"), cohort.parent_cohort_id] as [
            string,
            string,
          ],
        ]
      : []),
    [bt("bloem.admin.organization.cohorts.created"), formatDateTime(cohort.created_at)],
    ...(cohort.created_by_account_id
      ? [
          [
            bt("bloem.admin.organization.cohorts.createdBy"),
            bt("bloem.admin.organization.cohorts.account", {
              accountId: cohort.created_by_account_id,
            }),
          ] as [string, string],
        ]
      : []),
    [bt("bloem.admin.organization.cohorts.accessGroup"), String(cohort.access_group_id)],
    [bt("bloem.admin.organization.cohorts.cohortId"), cohort.cohort_id],
    [bt("bloem.admin.organization.cohorts.settingsFingerprint"), cohort.policy_digest],
  ];
  return (
    <article
      className="surface-panel space-y-4 rounded-2xl p-5"
      aria-label={bt("bloem.admin.organization.cohorts.cardLabel", {
        name: cohort.name,
        revision: cohort.revision,
      })}
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-lg font-medium">{cohort.name}</h2>
          <p className="text-muted-foreground mt-1 text-sm">{origin}</p>
        </div>
        <p className="text-muted-foreground text-sm tabular-nums">
          {cohort.archived
            ? bt("bloem.admin.organization.cohorts.memberCountArchived", {
                count: cohort.member_count,
              })
            : bt("bloem.admin.organization.cohorts.memberCount", { count: cohort.member_count })}
        </p>
      </header>

      <PolicySummary
        policy={cohort.policy}
        libraries={libraries}
        label={bt("bloem.admin.organization.cohorts.settings")}
      />

      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          {bt("bloem.admin.organization.cohorts.details")}
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
              aria-label={bt("bloem.admin.organization.cohorts.movePeopleTo", {
                name: cohort.name,
              })}
            >
              {bt("bloem.admin.organization.cohorts.movePeopleHere")}
            </Link>
          </Button>
        </div>
      ) : null}
    </article>
  );
}
