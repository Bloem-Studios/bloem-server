import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import AdminRequests from "@/pages/AdminRequests";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { captureAdminRequestContext } from "@/api/adminV2Client";
import { nativeApi } from "@/api/bloemClient";
import {
  bookRequestsApi,
  bookRequestState,
  type BookRequest,
  type BookRequestAction,
} from "@/api/bloemBookRequests";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RequestMediaTabs } from "./RequestMediaTabs";

export default function BloemAdminRequests() {
  const [params] = useSearchParams();
  const capabilities = useQuery({
    queryKey: ["bloem-capabilities"],
    queryFn: () => nativeApi<{ feature_tokens: string[] }>("/capabilities"),
  });
  const supported = capabilities.data?.feature_tokens.includes("audiobook_requests_v1");
  return (
    <>
      {supported && <RequestMediaTabs />}
      {supported && params.get("kind") === "audiobook" ? <AudiobookQueue /> : <AdminRequests />}
    </>
  );
}
function AudiobookQueue() {
  const { active, switching } = useAdminContext();
  const context = active ? captureAdminRequestContext(active.key) : null;
  const qc = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [reason, setReason] = useState<Record<string, string>>({});
  const list = useQuery({
    queryKey: ["bloem-admin-book-requests", active?.key, context?.generation, offset],
    queryFn: () => bookRequestsApi.adminList(context, offset),
    enabled: active?.scope === "organization" && !switching && !!context,
  });
  const action = useMutation({
    mutationFn: ({ row, kind }: { row: BookRequest; kind: BookRequestAction }) =>
      bookRequestsApi.adminAction(row, kind, reason[row.id] ?? "", context),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["bloem-admin-book-requests"] });
      toast.success("Request updated");
    },
    onError: (err) => {
      toast.error(err.message);
      void qc.invalidateQueries({ queryKey: ["bloem-admin-book-requests"] });
    },
  });
  return (
    <section className="space-y-6 px-4 py-6 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold">Audiobook requests</h1>
        <p className="text-muted-foreground text-sm">
          Review requests for {active?.name ?? "your organization"}. Approved titles wait for
          fulfilment.
        </p>
      </header>
      {active?.scope !== "organization" ? (
        <p role="status">
          Select an organization in the navigation to review its audiobook requests.
        </p>
      ) : (
        <>
          {list.isLoading && <p role="status">Loading requests…</p>}
          {list.isError && <p role="alert">{list.error.message}</p>}
          {list.data?.requests.length === 0 && (
            <p className="text-muted-foreground">No audiobook requests in this organization.</p>
          )}
          <ul className="divide-y rounded-xl border">
            {list.data?.requests.map((row) => (
              <li key={row.id} className="space-y-3 p-4">
                <div className="flex flex-wrap justify-between gap-2">
                  <div>
                    <p className="font-medium">{row.title}</p>
                    <p className="text-muted-foreground text-sm">{row.author}</p>
                  </div>
                  <p className="text-sm">{bookRequestState(row)}</p>
                </div>
                <p className="text-muted-foreground text-xs">
                  Account {row.requested_by_user_id} · Profile {row.requested_by_profile_id}
                  {row.isbn ? ` · ISBN ${row.isbn}` : ""}
                  {row.asin ? ` · ASIN ${row.asin}` : ""}
                </p>
                {row.note && <p className="text-sm">{row.note}</p>}
                {row.outcome_reason && (
                  <p className="text-muted-foreground text-sm">{row.outcome_reason}</p>
                )}
                {row.outcome === "active" && row.status !== "completed" && (
                  <>
                    <label className="block space-y-1 text-sm">
                      Reason (optional)
                      <Input
                        maxLength={2000}
                        value={reason[row.id] ?? ""}
                        onChange={(event) => setReason({ ...reason, [row.id]: event.target.value })}
                      />
                    </label>
                    <div className="flex flex-wrap gap-2">
                      {row.status === "pending" && (
                        <>
                          <Button
                            size="sm"
                            disabled={action.isPending || switching}
                            onClick={() => action.mutate({ row, kind: "approve" })}
                          >
                            Approve
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={action.isPending || switching}
                            onClick={() => action.mutate({ row, kind: "decline" })}
                          >
                            Decline
                          </Button>
                        </>
                      )}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={action.isPending || switching}
                        onClick={() => action.mutate({ row, kind: "fulfil" })}
                      >
                        Mark fulfilled
                      </Button>
                    </div>
                  </>
                )}
              </li>
            ))}
          </ul>
          <div className="flex gap-2">
            <Button
              variant="outline"
              disabled={offset === 0 || list.isFetching}
              onClick={() => setOffset(Math.max(0, offset - 100))}
            >
              Previous
            </Button>
            <Button
              variant="outline"
              disabled={list.data?.requests.length !== 100 || list.isFetching}
              onClick={() => setOffset(offset + 100)}
            >
              Next
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
