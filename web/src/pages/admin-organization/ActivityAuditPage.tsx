import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/bloemTypes";
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
  const { active } = useAdminContext();
  useDocumentTitle("Activity");
  if (active?.scope !== "organization")
    return <p role="alert">Choose an organization to see its activity.</p>;
  return <OrganizationAudit key={active.key} context={active} />;
}
function OrganizationAudit({ context }: { context: AdminContextSummary }) {
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
          Activity
        </h1>
        <p className="page-subtitle max-w-3xl">
          Changes to {context.name}: its name and status, its members, and their plans. Sign-ins,
          playback and other server events are not listed here.
        </p>
        <div className="flex flex-wrap gap-4 text-sm">
          <Link className="underline" to="/admin/organization/policy-decisions">
            Access checks
          </Link>
          <Link className="underline" to="/admin/organization/invitations">
            Invitations
          </Link>
        </div>
      </header>
      {query.isPending && <p role="status">Loading…</p>}
      {query.isError && (
        <div role="alert" className="space-y-3">
          <p className="text-destructive">Activity couldn’t be loaded.</p>
          <Button variant="outline" onClick={() => void query.refetch()}>
            Try again
          </Button>
        </div>
      )}
      {query.isSuccess && query.data.pages[0]?.events.length === 0 && (
        <p className="text-muted-foreground">Nothing has changed yet.</p>
      )}
      {query.data && (
        <ol className="divide-border divide-y border-y" aria-label="Activity">
          {query.data.pages
            .flatMap((page) => page.events)
            .map((event) => (
              <li key={`${event.source}:${event.id}`} className="space-y-2 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h2 className="font-medium">{activityLabel(event.action)}</h2>
                  {event.outcome !== "success" && event.outcome !== "succeeded" ? (
                    <Badge variant="outline">{outcomeLabel(event.outcome)}</Badge>
                  ) : null}
                </div>
                <p className="text-muted-foreground text-sm">
                  <time dateTime={event.created_at}>{formatDate(event.created_at)}</time> · By{" "}
                  {event.actor_account_id ? accountName(event.actor_account_id) : "Bloem"}
                </p>
                <details className="text-muted-foreground text-xs">
                  <summary className="hover:text-foreground cursor-pointer transition-colors">
                    Details
                  </summary>
                  <p className="mt-1 font-mono break-all">
                    {event.action} · {event.target_id}
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
          {query.isFetchingNextPage ? "Loading…" : "Show older"}
        </Button>
      )}
    </section>
  );
}

const ACTIVITY_LABELS: Record<string, string> = {
  "organization.created": "Organization created",
  "organization.renamed": "Renamed",
  "organization.slug_changed": "Short name changed",
  "organization.suspended": "Suspended",
  "organization.reactivated": "Reactivated",
  "organization.ownership_transferred": "Ownership transferred",
  "organization.entitlement_applied": "Plan applied",
  "membership.created": "Member added",
  "membership.role_changed": "Member's role changed",
  "membership.status_changed": "Member suspended or reactivated",
  "entitlement.policy_assignment": "Plan or settings changed for an account",
  "people.bulk_job_created": "Bulk change started",
  "people.bulk_policy_job_created": "Bulk plan or settings change started",
  "people.bulk_job_completed": "Bulk change finished",
  "people.bulk_job_failed": "Bulk change failed",
  "people.bulk_job_cancelled": "Bulk change stopped",
  "people.bulk_group_assigned": "Profiles moved to an access group",
  "people.bulk_policy_assigned": "Account moved to a new plan or settings",
  "people.bulk_membership_suspended": "Member suspended in a bulk change",
  "people.bulk_membership_reactivated": "Member reactivated in a bulk change",
  "people.bulk_record_skipped": "An account was skipped in a bulk change",
  "people.bulk_record_failed": "An account failed in a bulk change",
  "people.membership_updated": "Membership changed",
  "people.profile_group_updated": "Profile moved to another access group",
};

const OUTCOME_LABELS: Record<string, string> = {
  partial_failure: "Some not changed",
  conflict: "Not changed",
  failed: "Failed",
  failure: "Failed",
  skipped: "Skipped",
  denied: "Refused",
};

function outcomeLabel(outcome: string): string {
  return OUTCOME_LABELS[outcome] ?? outcome.replace(/_/g, " ");
}

function activityLabel(action: string): string {
  if (ACTIVITY_LABELS[action]) return ACTIVITY_LABELS[action]!;
  const text = action.replace(/[._]+/g, " ").trim();
  return text ? text[0]!.toUpperCase() + text.slice(1) : action;
}
