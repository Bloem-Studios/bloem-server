import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { toast } from "sonner";
import { useBloemT } from "@/bloem/i18n";
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
import { AudiobookAcquisitionPanel } from "./AudiobookAcquisitionPanel";
import { EbookRequestIntegrationPanel } from "./EbookRequestIntegrationPanel";

export default function BloemAdminRequests() {
  const bt = useBloemT();
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
          <h1 className="text-3xl font-semibold">{bt("bloem.requests.ebook.title")}</h1>
          <p role={capabilities.isError ? "alert" : "status"}>
            {capabilities.isPending
              ? bt("bloem.requests.ebook.checkingAvailability")
              : capabilities.isError
                ? bt("bloem.requests.ebook.availabilityCheckFailed")
                : bt("bloem.requests.ebook.unavailableOnServer")}
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
  const bt = useBloemT();
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
      throw new Error(bt("bloem.admin.requests.actionUnavailable"));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [cache] });
      toast.success(bt("bloem.admin.requests.updated"));
    },
    onError: (err) => {
      toast.error(err.message);
      void qc.invalidateQueries({ queryKey: [cache] });
    },
  });
  return (
    <section className="space-y-6 px-4 py-6 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold">
          {bt(ebook ? "bloem.requests.ebook.title" : "bloem.requests.audiobook.title")}
        </h1>
        <p className="text-muted-foreground text-sm">
          {active
            ? bt(
                ebook ? "bloem.admin.requests.ebook.intro" : "bloem.admin.requests.audiobook.intro",
                {
                  organization: active.name,
                },
              )
            : bt(
                ebook
                  ? "bloem.admin.requests.ebook.introNoOrganization"
                  : "bloem.admin.requests.audiobook.introNoOrganization",
              )}
        </p>
      </header>
      {ebook && active?.authority === "platform_admin" && <EbookRequestIntegrationPanel />}
      {active?.scope !== "organization" ? (
        <p role="status">
          {bt(
            ebook
              ? "bloem.admin.requests.ebook.selectOrganization"
              : "bloem.admin.requests.audiobook.selectOrganization",
          )}
        </p>
      ) : (
        <>
          {!ebook && <AudiobookAcquisitionPanel />}
          {ebook && (
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.requests.ebook.providerNote")}
            </p>
          )}
          {list.isLoading && <p role="status">{bt("bloem.requests.loading")}</p>}
          {list.isError && <p role="alert">{list.error.message}</p>}
          {list.data?.requests.length === 0 && (
            <p className="text-muted-foreground">
              {bt(
                ebook ? "bloem.admin.requests.ebook.empty" : "bloem.admin.requests.audiobook.empty",
              )}
            </p>
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
                  {bt("bloem.admin.requests.requester", {
                    // The account id is an identifier: Latin digits in every language.
                    account: String(row.requested_by_user_id),
                    profile: row.requested_by_profile_id,
                    identifiers:
                      row.isbn && row.asin
                        ? "both"
                        : row.isbn
                          ? "isbn"
                          : row.asin
                            ? "asin"
                            : "none",
                    isbn: row.isbn ?? "",
                    asin: row.asin ?? "",
                  })}
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
                        {bt("bloem.admin.requests.reasonOptional")}
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
                              {bt("bloem.admin.requests.approve")}
                            </Button>
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={action.isPending || switching}
                              onClick={() => action.mutate({ row, kind: "decline" })}
                            >
                              {bt("bloem.admin.requests.decline")}
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
                            {bt("bloem.admin.requests.markFulfilled")}
                          </Button>
                        )}
                        {ebook && row.status === "approved" && row.fulfilment === "failed" && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={action.isPending || switching}
                            onClick={() => action.mutate({ row, kind: "retry" })}
                          >
                            {bt("bloem.admin.requests.retryFulfilment")}
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
              {bt("bloem.requests.previousPage")}
            </Button>
            <Button
              variant="outline"
              disabled={list.data?.requests.length !== 100 || list.isFetching}
              onClick={() => setOffset(offset + 100)}
            >
              {bt("bloem.requests.nextPage")}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
