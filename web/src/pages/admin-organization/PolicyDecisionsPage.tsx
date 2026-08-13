import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ChevronRight, ShieldCheck, ShieldX } from "lucide-react";
import { useState } from "react";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import { Badge } from "@/components/ui/badge";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

interface DecisionExplanation {
  id: number;
  timestamp: string;
  organization: { id: string; membership_id?: string };
  subject: { account_id?: number; profile_id?: string };
  group: { id?: number; name?: string };
  library_ceiling: number[];
  action: string;
  resource: Record<string, unknown>;
  allowed: boolean;
  reason_code: string;
  policy_versions: Array<{ kind: string; name?: string; version: number }>;
}

export default function PolicyDecisionsPage() {
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const [selectedID, setSelectedID] = useState<number>();
  const decisions = useInfiniteQuery({
    queryKey: adminV2QueryKey(contextKey, "organization", "policy-decisions", "list"),
    queryFn: ({ pageParam }) =>
      adminV2Api<{ decisions: DecisionExplanation[]; next_cursor?: string }>(
        `/organization/policy-decisions?limit=50${
          pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""
        }`,
      ),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
  });
  const selected = useQuery({
    queryKey: adminV2QueryKey(contextKey, "organization", "policy-decisions", selectedID ?? "none"),
    queryFn: () =>
      adminV2Api<{ decision: DecisionExplanation }>(
        `/organization/policy-decisions/${selectedID}`,
      ).then((data) => data.decision),
    enabled: selectedID !== undefined,
  });
  useDocumentTitle("Policy Decisions");

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Policy Decisions
          </h1>
          <p className="page-subtitle">
            Read-only authorization explanations for {active?.name ?? "this organization"}.
          </p>
        </div>
      </div>

      {decisions.isLoading && <p className="text-muted-foreground text-sm">Loading decisions…</p>}
      {decisions.isError && <p role="alert">{decisions.error.message}</p>}
      <div className="grid gap-5 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
        <div className="space-y-2">
          {decisions.data?.pages
            .flatMap((page) => page.decisions)
            .map((decision) => (
              <button
                key={decision.id}
                type="button"
                onClick={() => setSelectedID(decision.id)}
                className="surface-panel focus-visible:ring-ring flex w-full items-center gap-3 rounded-xl p-4 text-left outline-none focus-visible:ring-2"
              >
                {decision.allowed ? (
                  <ShieldCheck className="size-5 text-emerald-500" aria-hidden />
                ) : (
                  <ShieldX className="text-destructive size-5" aria-hidden />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block font-medium">{decision.action}</span>
                  <span className="text-muted-foreground block text-xs">
                    {decision.allowed ? "Allowed" : "Denied"} ·{" "}
                    {decision.reason_code || "No reason code"}
                  </span>
                </span>
                <ChevronRight className="text-muted-foreground size-4" aria-hidden />
              </button>
            ))}
          {decisions.hasNextPage && (
            <button
              type="button"
              className="border-border hover:bg-muted w-full rounded-lg border px-3 py-2 text-sm font-medium"
              onClick={() => void decisions.fetchNextPage()}
              disabled={decisions.isFetchingNextPage}
            >
              {decisions.isFetchingNextPage ? "Loading more…" : "Load more decisions"}
            </button>
          )}
        </div>
        <div className="surface-panel min-h-48 rounded-2xl p-5">
          {!selectedID && (
            <p className="text-muted-foreground text-sm">
              Select a decision to inspect its explanation.
            </p>
          )}
          {selected.isLoading && (
            <p className="text-muted-foreground text-sm">Loading explanation…</p>
          )}
          {selected.data && <DecisionDetails decision={selected.data} />}
        </div>
      </div>
    </section>
  );
}

function DecisionDetails({ decision }: { decision: DecisionExplanation }) {
  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">Decision {decision.id}</h2>
        <Badge variant={decision.allowed ? "secondary" : "destructive"}>
          {decision.allowed ? "Allowed" : "Denied"}
        </Badge>
      </div>
      <dl className="grid gap-4 text-sm sm:grid-cols-2">
        <Detail label="Organization" value={decision.organization.id} />
        <Detail label="Membership" value={decision.organization.membership_id ?? "Not recorded"} />
        <Detail
          label="Subject"
          value={`Account ${decision.subject.account_id ?? "—"} · Profile ${decision.subject.profile_id ?? "—"}`}
        />
        <Detail
          label="Access group"
          value={
            decision.group.name
              ? `${decision.group.name} (${decision.group.id})`
              : String(decision.group.id ?? "Not resolved")
          }
        />
        <Detail
          label="Tenant library ceiling"
          value={decision.library_ceiling.length ? decision.library_ceiling.join(", ") : "None"}
        />
        <Detail label="Action" value={decision.action} />
        <Detail label="Reason code" value={decision.reason_code || "Not recorded"} />
      </dl>
      <div>
        <h3 className="text-sm font-semibold">Resource</h3>
        <dl className="mt-2 grid gap-2 text-sm">
          {Object.entries(decision.resource).map(([key, value]) => (
            <Detail key={key} label={key.replace(/_/g, " ")} value={formatValue(value)} />
          ))}
        </dl>
      </div>
      <div>
        <h3 className="text-sm font-semibold">Contributing policy versions</h3>
        <ul className="text-muted-foreground mt-2 space-y-1 text-sm">
          {decision.policy_versions.map((version) => (
            <li key={`${version.kind}:${version.name ?? ""}:${version.version}`}>
              {version.kind}
              {version.name ? ` ${version.name}` : ""} · version {version.version}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  const redacted = value === "[redacted]";
  return (
    <div>
      <dt className="text-muted-foreground capitalize">{label}</dt>
      <dd className="mt-0.5 font-medium break-all">
        {value} {redacted && <span className="sr-only">Sensitive value redacted</span>}
      </dd>
    </div>
  );
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "—";
  return JSON.stringify(value);
}
