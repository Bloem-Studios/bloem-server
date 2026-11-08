import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ChevronRight, ShieldCheck, ShieldX } from "lucide-react";
import { useMemo, useState } from "react";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import {
  accessActionLabel,
  accessReasonLabel,
  formatDateTime,
  libraryListLabel,
  type NamedLibrary,
} from "@/components/admin/policy/policyVocabulary";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
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
  const libraryQuery = useOrganizationLibraries(contextKey, Boolean(active));
  const libraries = useMemo<NamedLibrary[]>(
    () =>
      (libraryQuery.data ?? []).map((library) => ({ id: library.folder_id, name: library.name })),
    [libraryQuery.data],
  );
  useDocumentTitle("Access Checks");
  const rows = decisions.data?.pages.flatMap((page) => page.decisions) ?? [];

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Access Checks
          </h1>
          <p className="page-subtitle max-w-3xl">
            Each time someone in {active?.name ?? "this organization"} starts a video or a download,
            Bloem checks their settings and records the answer. Pick one to see why.
          </p>
        </div>
      </div>

      {decisions.isLoading ? <p className="text-muted-foreground text-sm">Loading…</p> : null}
      {decisions.isError ? (
        <p className="text-destructive text-sm" role="alert">
          Access checks couldn’t be loaded. Reload the page to try again.
        </p>
      ) : null}
      {!decisions.isLoading && !decisions.isError && rows.length === 0 ? (
        <p className="surface-panel text-muted-foreground rounded-2xl p-8 text-center text-sm">
          Nothing has been checked yet.
        </p>
      ) : null}
      {rows.length ? (
        <div className="grid gap-5 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
          <div className="space-y-2">
            {rows.map((decision) => (
              <button
                key={decision.id}
                type="button"
                aria-pressed={decision.id === selectedID}
                onClick={() => setSelectedID(decision.id)}
                className="surface-panel focus-visible:ring-ring hover:bg-muted/40 aria-pressed:border-primary flex w-full items-center gap-3 rounded-xl border border-transparent p-4 text-left transition-colors outline-none focus-visible:ring-2"
              >
                {decision.allowed ? (
                  <ShieldCheck className="size-5 shrink-0 text-emerald-600" aria-hidden />
                ) : (
                  <ShieldX className="text-destructive size-5 shrink-0" aria-hidden />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block font-medium">
                    {accessActionLabel(decision.action)}: {decision.allowed ? "allowed" : "refused"}
                  </span>
                  <span className="text-muted-foreground block truncate text-sm">
                    {decision.allowed ? "" : `${accessReasonLabel(decision.reason_code)} · `}
                    {formatDateTime(decision.timestamp)}
                  </span>
                </span>
                <ChevronRight className="text-muted-foreground size-4 shrink-0" aria-hidden />
              </button>
            ))}
            {decisions.hasNextPage ? (
              <button
                type="button"
                className="border-border hover:bg-muted w-full rounded-lg border px-3 py-2 text-sm font-medium transition-colors"
                onClick={() => void decisions.fetchNextPage()}
                disabled={decisions.isFetchingNextPage}
              >
                {decisions.isFetchingNextPage ? "Loading…" : "Show more"}
              </button>
            ) : null}
          </div>
          <div className="surface-panel min-h-48 rounded-2xl p-5 lg:sticky lg:top-4 lg:self-start">
            {selectedID === undefined ? (
              <p className="text-muted-foreground text-sm">Pick a check to see the details.</p>
            ) : selected.isLoading ? (
              <p className="text-muted-foreground text-sm">Loading…</p>
            ) : selected.isError ? (
              <p className="text-destructive text-sm" role="alert">
                This check couldn’t be loaded.
              </p>
            ) : selected.data ? (
              <DecisionDetails decision={selected.data} libraries={libraries} />
            ) : null}
          </div>
        </div>
      ) : null}
    </section>
  );
}

function DecisionDetails({
  decision,
  libraries,
}: {
  decision: DecisionExplanation;
  libraries: NamedLibrary[];
}) {
  const resource = Object.entries(decision.resource);
  return (
    <div className="space-y-5">
      <div>
        <h2 className="text-lg font-medium">
          {accessActionLabel(decision.action)}: {decision.allowed ? "allowed" : "refused"}
        </h2>
        <p className="text-muted-foreground text-sm">{formatDateTime(decision.timestamp)}</p>
      </div>
      {!decision.allowed ? (
        <p className="border-destructive/30 bg-destructive/10 rounded-lg border p-3 text-sm">
          {accessReasonLabel(decision.reason_code)}
        </p>
      ) : null}
      <dl className="grid gap-4 text-sm sm:grid-cols-2">
        <Detail
          label="Who"
          value={`Account ${decision.subject.account_id ?? "not recorded"}${
            decision.subject.profile_id ? ` · Profile ${decision.subject.profile_id}` : ""
          }`}
        />
        <Detail
          label="Access group"
          value={decision.group.name ?? (decision.group.id ? `Group ${decision.group.id}` : "None")}
        />
        <Detail
          label="Libraries they could use"
          value={libraryListLabel(decision.library_ceiling, libraries)}
          wide
        />
      </dl>
      {resource.length ? (
        <section>
          <h3 className="text-sm font-medium">What they asked for</h3>
          <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
            {resource.map(([key, value]) => (
              <Detail key={key} label={sentence(key)} value={formatValue(value)} />
            ))}
          </dl>
        </section>
      ) : null}
      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          Details
        </summary>
        <dl className="mt-3 grid gap-3 sm:grid-cols-2">
          <Detail label="Check" value={String(decision.id)} />
          <Detail label="Reason code" value={decision.reason_code || "None"} />
          <Detail label="Organization ID" value={decision.organization.id} />
          <Detail label="Membership ID" value={decision.organization.membership_id ?? "None"} />
          {decision.group.id ? (
            <Detail label="Access group ID" value={String(decision.group.id)} />
          ) : null}
          <Detail
            label="Rules checked"
            value={
              decision.policy_versions.length
                ? decision.policy_versions
                    .map(
                      (version) =>
                        `${version.kind}${version.name ? ` ${version.name}` : ""} version ${version.version}`,
                    )
                    .join(", ")
                : "None recorded"
            }
            wide
          />
        </dl>
      </details>
    </div>
  );
}

function Detail({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  const redacted = value === "[redacted]";
  return (
    <div className={wide ? "min-w-0 sm:col-span-2" : "min-w-0"}>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words">
        {value} {redacted ? <span className="sr-only">Sensitive value hidden</span> : null}
      </dd>
    </div>
  );
}

function sentence(key: string): string {
  const text = key.replace(/_/g, " ");
  return text ? text[0]!.toUpperCase() + text.slice(1) : key;
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "—";
  return JSON.stringify(value);
}
