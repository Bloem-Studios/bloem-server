import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/bloemTypes";
import { useBloemT, type BloemMessageKey, type BloemTranslate } from "@/bloem/i18n";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useAccountNames } from "@/components/admin/policy/useAccountNames";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { formatDate } from "@/lib/datetime";

interface AuditEvent {
  id: number;
  source: string;
  created_at: string;
  actor_account_id: number | null;
  action: string;
  target_id: string;
  outcome: string;
}
interface AuditPage {
  events: AuditEvent[];
  next_cursor?: string;
}

export default function ActivityAuditPage() {
  const bt = useBloemT();
  const { active } = useAdminContext();
  useDocumentTitle(bt("bloem.admin.organization.activity.title"));
  if (active?.scope !== "organization")
    return <p role="alert">{bt("bloem.admin.organization.activity.chooseOrganization")}</p>;
  return <OrganizationAudit key={active.key} context={active} />;
}
function OrganizationAudit({ context }: { context: AdminContextSummary }) {
  const bt = useBloemT();
  const authority = captureAdminRequestContext(context.key);
  const accountName = useAccountNames();
  const query = useInfiniteQuery({
    queryKey: adminV2QueryKey(context.key, "organization", "activity", authority?.generation),
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      adminV2Api<AuditPage>(
        `/organization/activity${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ""}`,
        { signal },
        "safe",
        authority,
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    retry: false,
  });
  return (
    <section className="admin-page space-y-6">
      <header className="space-y-2">
        <h1 className="page-title" tabIndex={-1}>
          {bt("bloem.admin.organization.activity.title")}
        </h1>
        <p className="page-subtitle max-w-3xl">
          {bt("bloem.admin.organization.activity.subtitle", { organization: context.name })}
        </p>
        <div className="flex flex-wrap gap-4 text-sm">
          <Link className="underline" to="/admin/organization/policy-decisions">
            {bt("bloem.admin.organization.activity.accessChecks")}
          </Link>
          <Link className="underline" to="/admin/organization/invitations">
            {bt("bloem.admin.organization.activity.invitations")}
          </Link>
        </div>
      </header>
      {query.isPending && <p role="status">{bt("bloem.admin.organization.activity.loading")}</p>}
      {query.isError && (
        <div role="alert" className="space-y-3">
          <p className="text-destructive">{bt("bloem.admin.organization.activity.loadFailed")}</p>
          <Button variant="outline" onClick={() => void query.refetch()}>
            {bt("bloem.admin.organization.activity.tryAgain")}
          </Button>
        </div>
      )}
      {query.isSuccess && query.data.pages[0]?.events.length === 0 && (
        <p className="text-muted-foreground">{bt("bloem.admin.organization.activity.empty")}</p>
      )}
      {query.data && (
        <ol
          className="divide-border divide-y border-y"
          aria-label={bt("bloem.admin.organization.activity.title")}
        >
          {query.data.pages
            .flatMap((page) => page.events)
            .map((event) => (
              <li key={`${event.source}:${event.id}`} className="space-y-2 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h2 className="font-medium">{activityLabel(event.action, bt)}</h2>
                  {event.outcome !== "success" && event.outcome !== "succeeded" ? (
                    <Badge variant="outline">{outcomeLabel(event.outcome, bt)}</Badge>
                  ) : null}
                </div>
                <p className="text-muted-foreground text-sm">
                  {event.actor_account_id
                    ? bt("bloem.admin.organization.activity.byAccount", {
                        date: formatDate(event.created_at),
                        actor: accountName(event.actor_account_id),
                        time: (chunks) => <time dateTime={event.created_at}>{chunks}</time>,
                      })
                    : bt("bloem.admin.organization.activity.byBloem", {
                        date: formatDate(event.created_at),
                        time: (chunks) => <time dateTime={event.created_at}>{chunks}</time>,
                      })}
                </p>
                <details className="text-muted-foreground text-xs">
                  <summary className="hover:text-foreground cursor-pointer transition-colors">
                    {bt("bloem.admin.organization.activity.details")}
                  </summary>
                  <p className="mt-1 font-mono break-all">
                    {bt("bloem.admin.organization.activity.technical", {
                      action: event.action,
                      targetId: event.target_id,
                    })}
                  </p>
                </details>
              </li>
            ))}
        </ol>
      )}
      {query.hasNextPage && (
        <Button
          variant="outline"
          disabled={query.isFetching}
          onClick={() => void query.fetchNextPage()}
        >
          {query.isFetchingNextPage
            ? bt("bloem.admin.organization.activity.loading")
            : bt("bloem.admin.organization.activity.showOlder")}
        </Button>
      )}
    </section>
  );
}

// Event codes and outcomes to their labels' message ids; the text is read when shown.
const ACTIVITY_LABELS: Record<string, BloemMessageKey> = {
  "organization.created": "bloem.admin.organization.activity.action.organizationCreated",
  "organization.renamed": "bloem.admin.organization.activity.action.organizationRenamed",
  "organization.slug_changed": "bloem.admin.organization.activity.action.organizationSlugChanged",
  "organization.suspended": "bloem.admin.organization.activity.action.organizationSuspended",
  "organization.reactivated": "bloem.admin.organization.activity.action.organizationReactivated",
  "organization.ownership_transferred":
    "bloem.admin.organization.activity.action.organizationOwnershipTransferred",
  "organization.entitlement_applied":
    "bloem.admin.organization.activity.action.organizationEntitlementApplied",
  "membership.created": "bloem.admin.organization.activity.action.membershipCreated",
  "membership.role_changed": "bloem.admin.organization.activity.action.membershipRoleChanged",
  "membership.status_changed": "bloem.admin.organization.activity.action.membershipStatusChanged",
  "entitlement.policy_assignment":
    "bloem.admin.organization.activity.action.entitlementPolicyAssignment",
  "people.bulk_job_created": "bloem.admin.organization.activity.action.peopleBulkJobCreated",
  "people.bulk_policy_job_created":
    "bloem.admin.organization.activity.action.peopleBulkPolicyJobCreated",
  "people.bulk_job_completed": "bloem.admin.organization.activity.action.peopleBulkJobCompleted",
  "people.bulk_job_failed": "bloem.admin.organization.activity.action.peopleBulkJobFailed",
  "people.bulk_job_cancelled": "bloem.admin.organization.activity.action.peopleBulkJobCancelled",
  "people.bulk_group_assigned": "bloem.admin.organization.activity.action.peopleBulkGroupAssigned",
  "people.bulk_policy_assigned":
    "bloem.admin.organization.activity.action.peopleBulkPolicyAssigned",
  "people.bulk_membership_suspended":
    "bloem.admin.organization.activity.action.peopleBulkMembershipSuspended",
  "people.bulk_membership_reactivated":
    "bloem.admin.organization.activity.action.peopleBulkMembershipReactivated",
  "people.bulk_record_skipped": "bloem.admin.organization.activity.action.peopleBulkRecordSkipped",
  "people.bulk_record_failed": "bloem.admin.organization.activity.action.peopleBulkRecordFailed",
  "people.membership_updated": "bloem.admin.organization.activity.action.peopleMembershipUpdated",
  "people.profile_group_updated":
    "bloem.admin.organization.activity.action.peopleProfileGroupUpdated",
};

const OUTCOME_LABELS: Record<string, BloemMessageKey> = {
  partial_failure: "bloem.admin.organization.activity.outcome.partialFailure",
  conflict: "bloem.admin.organization.activity.outcome.conflict",
  failed: "bloem.admin.organization.activity.outcome.failed",
  failure: "bloem.admin.organization.activity.outcome.failed",
  skipped: "bloem.admin.organization.activity.outcome.skipped",
  denied: "bloem.admin.organization.activity.outcome.denied",
};

function outcomeLabel(outcome: string, bt: BloemTranslate): string {
  const id = OUTCOME_LABELS[outcome];
  return id ? bt(id) : outcome.replace(/_/g, " ");
}

function activityLabel(action: string, bt: BloemTranslate): string {
  const id = ACTIVITY_LABELS[action];
  if (id) return bt(id);
  const text = action.replace(/[._]+/g, " ").trim();
  return text ? text[0]!.toUpperCase() + text.slice(1) : action;
}
