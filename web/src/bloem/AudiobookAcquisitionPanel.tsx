import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { audiobookAcquisitionsApi, type AcquisitionFilter } from "@/api/bloemAudiobookAcquisitions";
import { Button } from "@/components/ui/button";
import { AudiobookRequestIntegrationPanel } from "./AudiobookRequestIntegrationPanel";
import { AudiobookAcquisitionDetails } from "./AudiobookAcquisitionDetails";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
export function AudiobookAcquisitionPanel() {
  const scope = useAudiobookAdminScope();
  return scope.allowed ? <ScopedAcquisitionPanel key={scope.key} /> : null;
}
function ScopedAcquisitionPanel() {
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
      <section className="space-y-4" aria-label="Audiobook acquisition review">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div className="space-y-1">
            <h2 className="text-xl font-semibold">Acquisition review</h2>
            <p className="text-muted-foreground text-sm">
              Inspect provider progress and review held releases or downloaded audio.
            </p>
          </div>
          <label className="grid gap-2 text-sm font-medium">
            Acquisition state
            <select
              value={filter}
              className="border-input h-9 rounded-md border bg-transparent px-3"
              onChange={(event) => {
                setFilter(event.target.value as AcquisitionFilter);
                setCursors([""]);
                setSelected("");
              }}
            >
              {["all", "active", "review", "failed", "stuck"].map((state) => (
                <option key={state} value={state}>
                  {state === "all"
                    ? "All acquisitions"
                    : state.charAt(0).toUpperCase() + state.slice(1)}
                </option>
              ))}
            </select>
          </label>
        </header>
        {list.isPending && <p role="status">Loading acquisitions…</p>}
        {list.isError && (
          <div className="space-y-2">
            <p role="alert">Unable to load acquisitions.</p>
            <Button variant="outline" onClick={() => void list.refetch()}>
              Reload acquisitions
            </Button>
          </div>
        )}
        {list.data?.acquisitions.length === 0 && (
          <p role="status">No acquisitions match this state.</p>
        )}
        <ul className="divide-y rounded-xl border">
          {list.data?.acquisitions.map((row) => (
            <li key={row.request.id} className="space-y-3 p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-medium break-words">{row.request.title}</p>
                  <p className="text-muted-foreground text-sm">
                    {row.request.author} ·{" "}
                    {row.provider_status.replaceAll("_", " ") || "Not started"}
                    {row.review_kind ? ` · ${row.review_kind} review` : ""}
                    {row.stuck
                      ? ` · ${row.stuck_reason?.replaceAll("_", " ") || "Needs attention"}`
                      : ""}
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Review acquisition ${row.request.title}`}
                  aria-expanded={selected === row.request.id}
                  onClick={() => setSelected(selected === row.request.id ? "" : row.request.id)}
                >
                  {selected === row.request.id ? "Close review" : "Review acquisition"}
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
            Previous acquisitions
          </Button>
          <Button
            variant="outline"
            disabled={!list.data?.next_cursor || list.isFetching}
            onClick={() => {
              if (list.data?.next_cursor) setCursors([...cursors, list.data.next_cursor]);
              setSelected("");
            }}
          >
            Next acquisitions
          </Button>
        </div>
      </section>
    </div>
  );
}
