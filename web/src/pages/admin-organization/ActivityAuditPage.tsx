import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { adminV2Api, adminV2QueryKey, captureAdminRequestContext } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/types";
import { useAdminContext } from "@/contexts/AdminContextProvider";
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
  useDocumentTitle("Organization audit");
  if (active?.scope !== "organization")
    return <p role="alert">Select an organization to view its audit history.</p>;
  return <OrganizationAudit key={active.key} context={active} />;
}
function OrganizationAudit({ context }: { context: AdminContextSummary }) {
  const authority = captureAdminRequestContext(context.key);
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
          Organization audit
        </h1>
        <p className="page-subtitle">
          Organization, membership and entitlement lifecycle events for {context.name}. This is not
          a server-wide activity log.
        </p>
        <div className="flex flex-wrap gap-4 text-sm">
          <Link className="underline" to="/admin/organization/policy-decisions">
            Authorization decisions
          </Link>
          <Link className="underline" to="/admin/organization/invitations">
            Invitation history
          </Link>
        </div>
      </header>
      {query.isPending && <p role="status">Loading audit history…</p>}
      {query.isError && (
        <div role="alert" className="space-y-3">
          <p className="text-destructive">{query.error.message}</p>
          <Button variant="outline" onClick={() => void query.refetch()}>
            Reload audit history
          </Button>
        </div>
      )}
      {query.isSuccess && query.data.pages[0]?.events.length === 0 && (
        <p className="text-muted-foreground">No recorded lifecycle events for this organization.</p>
      )}
      {query.data && (
        <ol className="divide-border divide-y border-y" aria-label="Organization audit events">
          {query.data.pages
            .flatMap((page) => page.events)
            .map((event) => (
              <li key={`${event.source}:${event.id}`} className="space-y-2 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h2 className="font-medium">{event.action}</h2>
                  <Badge variant="outline">{event.outcome}</Badge>
                </div>
                <p className="text-muted-foreground text-sm">
                  <time dateTime={event.created_at}>{formatDate(event.created_at)}</time> ·{" "}
                  {event.source} ·{" "}
                  {event.actor_account_id ? `Account ${event.actor_account_id}` : "System"}
                </p>
                <p className="text-muted-foreground text-sm break-all">Target: {event.target_id}</p>
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
          {query.isFetchingNextPage ? "Loading…" : "Load older events"}
        </Button>
      )}
    </section>
  );
}
