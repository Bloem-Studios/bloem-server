import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { ChevronRight, ShieldCheck, ShieldX } from "lucide-react";
import { useMemo, useState } from "react";

import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
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
  useDocumentTitle(bt("bloem.admin.organization.decisions.title"));
  const rows = decisions.data?.pages.flatMap((page) => page.decisions) ?? [];

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.organization.decisions.title")}
          </h1>
          <p className="page-subtitle max-w-3xl">
            {bt("bloem.admin.organization.decisions.subtitle", {
              organization: active?.name ?? bt("bloem.admin.organization.thisOrganization"),
            })}
          </p>
        </div>
      </div>

      {decisions.isLoading ? (
        <p className="text-muted-foreground text-sm">
          {bt("bloem.admin.organization.decisions.loading")}
        </p>
      ) : null}
      {decisions.isError ? (
        <p className="text-destructive text-sm" role="alert">
          {bt("bloem.admin.organization.decisions.loadFailed")}
        </p>
      ) : null}
      {!decisions.isLoading && !decisions.isError && rows.length === 0 ? (
        <p className="surface-panel text-muted-foreground rounded-2xl p-8 text-center text-sm">
          {bt("bloem.admin.organization.decisions.empty")}
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
                className="surface-panel focus-visible:ring-ring hover:bg-muted/40 aria-pressed:border-primary flex w-full items-center gap-3 rounded-xl border border-transparent p-4 text-start transition-colors outline-none focus-visible:ring-2"
              >
                {decision.allowed ? (
                  <ShieldCheck className="size-5 shrink-0 text-emerald-600" aria-hidden />
                ) : (
                  <ShieldX className="text-destructive size-5 shrink-0" aria-hidden />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block font-medium">
                    {bt(
                      decision.allowed
                        ? "bloem.admin.organization.decisions.allowed"
                        : "bloem.admin.organization.decisions.refused",
                      {
                        action: accessActionLabel(decision.action),
                      },
                    )}
                  </span>
                  <span className="text-muted-foreground block truncate text-sm">
                    {decision.allowed
                      ? formatDateTime(decision.timestamp)
                      : bt("bloem.admin.organization.decisions.reasonAndTime", {
                          reason: accessReasonLabel(decision.reason_code),
                          time: formatDateTime(decision.timestamp),
                        })}
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
                {decisions.isFetchingNextPage
                  ? bt("bloem.admin.organization.decisions.loading")
                  : bt("bloem.admin.organization.decisions.showMore")}
              </button>
            ) : null}
          </div>
          <div className="surface-panel min-h-48 rounded-2xl p-5 lg:sticky lg:top-4 lg:self-start">
            {selectedID === undefined ? (
              <p className="text-muted-foreground text-sm">
                {bt("bloem.admin.organization.decisions.pickOne")}
              </p>
            ) : selected.isLoading ? (
              <p className="text-muted-foreground text-sm">
                {bt("bloem.admin.organization.decisions.loading")}
              </p>
            ) : selected.isError ? (
              <p className="text-destructive text-sm" role="alert">
                {bt("bloem.admin.organization.decisions.detailLoadFailed")}
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
  const bt = useBloemT();
  const resource = Object.entries(decision.resource);
  const accountId = decision.subject.account_id ?? null;
  const account =
    accountId === null
      ? bt("bloem.admin.organization.decisions.accountNotRecorded")
      : bt("bloem.admin.organization.decisions.account", { accountId });
  const none = bt("bloem.admin.organization.decisions.none");
  return (
    <div className="space-y-5">
      <div>
        <h2 className="text-lg font-medium">
          {bt(
            decision.allowed
              ? "bloem.admin.organization.decisions.allowed"
              : "bloem.admin.organization.decisions.refused",
            {
              action: accessActionLabel(decision.action),
            },
          )}
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
          label={bt("bloem.admin.organization.decisions.who")}
          value={
            decision.subject.profile_id
              ? bt("bloem.admin.organization.decisions.withProfile", {
                  account,
                  profileId: decision.subject.profile_id,
                })
              : account
          }
        />
        <Detail
          label={bt("bloem.admin.organization.decisions.accessGroup")}
          value={
            decision.group.name ??
            (decision.group.id
              ? bt("bloem.admin.organization.decisions.groupNumber", { groupId: decision.group.id })
              : none)
          }
        />
        <Detail
          label={bt("bloem.admin.organization.decisions.libraries")}
          value={libraryListLabel(decision.library_ceiling, libraries)}
          wide
        />
      </dl>
      {resource.length ? (
        <section>
          <h3 className="text-sm font-medium">
            {bt("bloem.admin.organization.decisions.requested")}
          </h3>
          <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
            {resource.map(([key, value]) => (
              <Detail key={key} label={sentence(key)} value={formatValue(value)} />
            ))}
          </dl>
        </section>
      ) : null}
      <details className="text-sm">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
          {bt("bloem.admin.organization.decisions.details")}
        </summary>
        <dl className="mt-3 grid gap-3 sm:grid-cols-2">
          <Detail
            label={bt("bloem.admin.organization.decisions.check")}
            value={String(decision.id)}
          />
          <Detail
            label={bt("bloem.admin.organization.decisions.reasonCode")}
            value={decision.reason_code || none}
          />
          <Detail
            label={bt("bloem.admin.organization.decisions.organizationId")}
            value={decision.organization.id}
          />
          <Detail
            label={bt("bloem.admin.organization.decisions.membershipId")}
            value={decision.organization.membership_id ?? none}
          />
          {decision.group.id ? (
            <Detail
              label={bt("bloem.admin.organization.decisions.accessGroupId")}
              value={String(decision.group.id)}
            />
          ) : null}
          <Detail
            label={bt("bloem.admin.organization.decisions.rulesChecked")}
            value={
              decision.policy_versions.length
                ? decision.policy_versions
                    .map((version) =>
                      version.name
                        ? bt("bloem.admin.organization.decisions.ruleVersionNamed", {
                            kind: version.kind,
                            name: version.name,
                            version: version.version,
                          })
                        : bt("bloem.admin.organization.decisions.ruleVersion", {
                            kind: version.kind,
                            version: version.version,
                          }),
                    )
                    .join(", ")
                : bt("bloem.admin.organization.decisions.noneRecorded")
            }
            wide
          />
        </dl>
      </details>
    </div>
  );
}

function Detail({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  const bt = useBloemT();
  const hidden =
    value === "[redacted]" ? (
      <span className="sr-only">{bt("bloem.admin.organization.decisions.sensitiveHidden")}</span>
    ) : null;
  return (
    <div className={wide ? "min-w-0 sm:col-span-2" : "min-w-0"}>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words">
        {value} {hidden}
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
