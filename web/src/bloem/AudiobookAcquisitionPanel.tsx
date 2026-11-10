import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { audiobookAcquisitionsApi, type AcquisitionFilter } from "@/api/bloemAudiobookAcquisitions";
import { useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { AudiobookRequestIntegrationPanel } from "./AudiobookRequestIntegrationPanel";
import { AudiobookAcquisitionDetails } from "./AudiobookAcquisitionDetails";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
// A divider glyph between facts, the same in every language.
const DIVIDER = " · ";
const states: [AcquisitionFilter, BloemMessageKey][] = [
  ["all", "bloem.admin.bookRequests.acquisitions.state.all"],
  ["active", "bloem.admin.bookRequests.acquisitions.state.active"],
  ["review", "bloem.admin.bookRequests.acquisitions.state.review"],
  ["failed", "bloem.admin.bookRequests.acquisitions.state.failed"],
  ["stuck", "bloem.admin.bookRequests.acquisitions.state.stuck"],
];
export function AudiobookAcquisitionPanel() {
  const scope = useAudiobookAdminScope();
  return scope.allowed ? <ScopedAcquisitionPanel key={scope.key} /> : null;
}
function ScopedAcquisitionPanel() {
  const bt = useBloemT();
  const scope = useAudiobookAdminScope();
  const [filter, setFilter] = useState<AcquisitionFilter>("all");
  const [cursors, setCursors] = useState([""]);
  const [selected, setSelected] = useState("");
  const cursor = cursors[cursors.length - 1];
  const list = useQuery({
    queryKey: ["bloem-audiobook-acquisitions", scope.key, filter, cursor],
    queryFn: ({ signal }) => audiobookAcquisitionsApi.list(scope.context, filter, cursor, signal),
    enabled: scope.allowed,
    retry: false,
  });
  return (
    <div className="space-y-6">
      <AudiobookRequestIntegrationPanel />
      <section
        className="space-y-4"
        aria-label={bt("bloem.admin.bookRequests.acquisitions.sectionLabel")}
      >
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div className="space-y-1">
            <h2 className="text-xl font-semibold">
              {bt("bloem.admin.bookRequests.acquisitions.title")}
            </h2>
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.bookRequests.acquisitions.intro")}
            </p>
          </div>
          <label className="grid gap-2 text-sm font-medium">
            {bt("bloem.admin.bookRequests.acquisitions.stateLabel")}
            <select
              value={filter}
              className="border-input h-9 rounded-md border bg-transparent px-3"
              onChange={(event) => {
                setFilter(event.target.value as AcquisitionFilter);
                setCursors([""]);
                setSelected("");
              }}
            >
              {states.map(([state, label]) => (
                <option key={state} value={state}>
                  {bt(label)}
                </option>
              ))}
            </select>
          </label>
        </header>
        {list.isPending && (
          <p role="status">{bt("bloem.admin.bookRequests.acquisitions.loading")}</p>
        )}
        {list.isError && (
          <div className="space-y-2">
            <p role="alert">{bt("bloem.admin.bookRequests.acquisitions.loadFailed")}</p>
            <Button variant="outline" onClick={() => void list.refetch()}>
              {bt("bloem.admin.bookRequests.acquisitions.reload")}
            </Button>
          </div>
        )}
        {list.data?.acquisitions.length === 0 && (
          <p role="status">{bt("bloem.admin.bookRequests.acquisitions.empty")}</p>
        )}
        <ul className="divide-y rounded-xl border">
          {list.data?.acquisitions.map((row) => (
            <li key={row.request.id} className="space-y-3 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-medium break-words">{row.request.title}</p>
                  <p className="text-muted-foreground text-sm">
                    {[
                      row.request.author,
                      row.provider_status.replaceAll("_", " ") ||
                        bt("bloem.admin.bookRequests.notStarted"),
                      ...(row.review_kind
                        ? [
                            bt("bloem.admin.bookRequests.acquisitions.reviewKind", {
                              kind: row.review_kind,
                            }),
                          ]
                        : []),
                      ...(row.stuck
                        ? [
                            row.stuck_reason?.replaceAll("_", " ") ||
                              bt("bloem.admin.bookRequests.acquisitions.needsAttention"),
                          ]
                        : []),
                    ].join(DIVIDER)}
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={bt("bloem.admin.bookRequests.acquisitions.reviewLabel", {
                    title: row.request.title,
                  })}
                  aria-expanded={selected === row.request.id}
                  onClick={() => setSelected(selected === row.request.id ? "" : row.request.id)}
                >
                  {selected === row.request.id
                    ? bt("bloem.admin.bookRequests.acquisitions.closeReview")
                    : bt("bloem.admin.bookRequests.acquisitions.review")}
                </Button>
              </div>
              {selected === row.request.id && (
                <AudiobookAcquisitionDetails
                  id={row.request.id}
                  key={`${scope.key}:${row.request.id}`}
                />
              )}
            </li>
          ))}
        </ul>
        <div className="flex gap-2">
          <Button
            variant="outline"
            disabled={cursors.length === 1 || list.isFetching}
            onClick={() => {
              setCursors(cursors.slice(0, -1));
              setSelected("");
            }}
          >
            {bt("bloem.admin.bookRequests.acquisitions.previous")}
          </Button>
          <Button
            variant="outline"
            disabled={!list.data?.next_cursor || list.isFetching}
            onClick={() => {
              if (list.data?.next_cursor) setCursors([...cursors, list.data.next_cursor]);
              setSelected("");
            }}
          >
            {bt("bloem.admin.bookRequests.acquisitions.next")}
          </Button>
        </div>
      </section>
    </div>
  );
}
