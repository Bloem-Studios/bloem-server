import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import AdminRequests from "@/pages/AdminRequests";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { captureAdminRequestContext } from "@/api/adminV2Client";
import { captureSessionIdentity } from "@/api/client";
import { nativeApi } from "@/api/bloemClient";
import {
  bookRequestsApi,
  ebookRequestsApi,
  bookRequestState,
  type BookRequest,
  type BookRequestAction,
  type EbookRequestAction,
} from "@/api/bloemBookRequests";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RequestMediaTabs } from "./RequestMediaTabs";
import { EbookRequestIntegrationPanel } from "./EbookRequestIntegrationPanel";

export default function BloemAdminRequests() {
  const [params] = useSearchParams();
  const identity = captureSessionIdentity();
  const { active } = useAdminContext();
  const context = active ? captureAdminRequestContext(active.key) : null;
  const capabilities = useQuery({
    queryKey: ["bloem-capabilities", identity.serverOrigin, identity.authContextVersion],
    queryFn: () => nativeApi<{ feature_tokens: string[] }>("/capabilities"),
  });
  const audioSupported =
    capabilities.data?.feature_tokens.includes("audiobook_requests_v1") ?? false;
  const ebookSupported = capabilities.data?.feature_tokens.includes("ebook_requests_v1") ?? false;
  const ebook = params.get("kind") === "ebook";
  const audio = params.get("kind") === "audiobook";
  return (
    <>
      {(audioSupported || ebookSupported) && (
        <RequestMediaTabs audiobooks={audioSupported} ebooks={ebookSupported} />
      )}
      {ebook && !ebookSupported ? (
        <section className="space-y-4 px-4 py-6 sm:px-6">
          <h1 className="text-3xl font-semibold">Ebook requests</h1>
          <p role={capabilities.isError ? "alert" : "status"}>
            {capabilities.isPending
              ? "Checking ebook request availability…"
              : capabilities.isError
                ? "Unable to check ebook request availability."
                : "Ebook requests are unavailable on this server."}
          </p>
        </section>
      ) : (ebook && ebookSupported) || (audio && audioSupported) ? (
        <BookQueue
          ebook={ebook}
          key={JSON.stringify([
            ebook,
            context?.key,
            context?.generation,
            identity.serverOrigin,
            identity.authContextVersion,
          ])}
        />
      ) : (
        <AdminRequests />
      )}
    </>
  );
}
function BookQueue({ ebook }: { ebook: boolean }) {
  const label = ebook ? "Ebook" : "Audiobook";
  const noun = label.toLowerCase();
  const api = ebook ? ebookRequestsApi : bookRequestsApi;
  const cache = ebook ? "bloem-admin-ebook-requests" : "bloem-admin-book-requests";
  const { active, switching } = useAdminContext();
  const identity = captureSessionIdentity();
  const context = active ? captureAdminRequestContext(active.key) : null;
  const qc = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [reason, setReason] = useState<Record<string, string>>({});
  const list = useQuery({
    queryKey: [
      cache,
      active?.key,
      context?.generation,
      identity.serverOrigin,
      identity.authContextVersion,
      offset,
    ],
    queryFn: () => api.adminList(context, offset),
    enabled: active?.scope === "organization" && !switching && !!context,
  });
  const action = useMutation({
    mutationFn: ({
      row,
      kind,
    }: {
      row: BookRequest;
      kind: BookRequestAction | EbookRequestAction;
    }) => {
      if (ebook && kind !== "fulfil" && kind !== "cancel") {
        return ebookRequestsApi.adminAction(row, kind, reason[row.id] ?? "", context);
      }
      if (!ebook && kind !== "retry") {
        return bookRequestsApi.adminAction(row, kind, reason[row.id] ?? "", context);
      }
      throw new Error("This action is unavailable for this request type.");
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [cache] });
      toast.success("Request updated");
    },
    onError: (err) => {
      toast.error(err.message);
      void qc.invalidateQueries({ queryKey: [cache] });
    },
  });
  return (
    <section className="space-y-6 px-4 py-6 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold">{label} requests</h1>
        <p className="text-muted-foreground text-sm">
          Review requests for {active?.name ?? "your organization"}. Approved titles wait for
          fulfilment{ebook ? " and library availability" : ""}.
        </p>
      </header>
      {active?.scope !== "organization" ? (
        <p role="status">Select an organization in the navigation to review its {noun} requests.</p>
      ) : (
        <>
          {ebook && active?.authority === "platform_admin" && <EbookRequestIntegrationPanel />}
          {ebook && (
            <p className="text-muted-foreground text-sm">
              Provider progress is shown below. Ebooks become fulfilled only when they are available
              in the linked library. Cancellation is local to Bloem; provider processing may
              continue.
            </p>
          )}
          {list.isLoading && <p role="status">Loading requests…</p>}
          {list.isError && <p role="alert">{list.error.message}</p>}
          {list.data?.requests.length === 0 && (
            <p className="text-muted-foreground">No {noun} requests in this organization.</p>
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
                {row.outcome === "active" &&
                  row.status !== "completed" &&
                  (!ebook || row.status === "pending" || row.fulfilment === "failed") && (
                    <>
                      <label className="block space-y-1 text-sm">
                        Reason (optional)
                        <Input
                          maxLength={2000}
                          value={reason[row.id] ?? ""}
                          onChange={(event) =>
                            setReason({ ...reason, [row.id]: event.target.value })
                          }
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
                        {!ebook && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={action.isPending || switching}
                            onClick={() => action.mutate({ row, kind: "fulfil" })}
                          >
                            Mark fulfilled
                          </Button>
                        )}
                        {ebook && row.status === "approved" && row.fulfilment === "failed" && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={action.isPending || switching}
                            onClick={() => action.mutate({ row, kind: "retry" })}
                          >
                            Retry fulfilment
                          </Button>
                        )}
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
