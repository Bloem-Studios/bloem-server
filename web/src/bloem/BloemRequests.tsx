import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { toast } from "sonner";
import Requests from "@/pages/Requests";
import { useAuth } from "@/hooks/useAuth";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { captureProfileRequestContext, captureSessionIdentity } from "@/api/client";
import { nativeApi } from "@/api/bloemClient";
import {
  bookRequestsApi,
  ebookRequestsApi,
  bookRequestState,
  type BookRequest,
  type BookRequestInput,
} from "@/api/bloemBookRequests";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { randomUUID } from "@/lib/uuid";
import { RequestMediaTabs } from "./RequestMediaTabs";

export default function BloemRequests() {
  const [params] = useSearchParams();
  const { user, profile } = useAuth();
  const identity = captureSessionIdentity();
  const context = captureProfileRequestContext();
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
        <section className="mx-auto max-w-4xl space-y-4 px-4 py-6 sm:px-6">
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
        <BookRequests
          ebook={ebook}
          key={JSON.stringify([
            ebook,
            user?.id,
            profile?.id,
            context?.serverOrigin,
            context?.authContextVersion,
            context?.profileId,
            context?.profileTokenGeneration,
          ])}
        />
      ) : (
        <Requests />
      )}
    </>
  );
}

const emptyInput: BookRequestInput = { title: "", author: "", isbn: "", asin: "", note: "" };
function BookRequests({ ebook }: { ebook: boolean }) {
  const label = ebook ? "Ebook" : "Audiobook";
  const noun = label.toLowerCase();
  const api = ebook ? ebookRequestsApi : bookRequestsApi;
  const cache = ebook ? "bloem-ebook-requests" : "bloem-book-requests";
  useDocumentTitle(`${label} requests`);
  const { user, profile } = useAuth();
  const qc = useQueryClient();
  const context = captureProfileRequestContext();
  const scope = [
    cache,
    user?.id,
    profile?.id,
    context?.authContextVersion,
    context?.serverOrigin,
    context?.profileTokenGeneration,
  ] as const;
  const capability = useQuery({
    queryKey: [...scope, "capability"],
    queryFn: () => api.capability(context),
    enabled: !!context,
  });
  const [offset, setOffset] = useState(0);
  const mine = useQuery({
    queryKey: [...scope, "mine", offset],
    queryFn: () => api.mine(context, offset),
    enabled: !!context,
  });
  const [input, setInput] = useState<BookRequestInput>(emptyInput);
  const key = useRef<{ input: string; key: string } | null>(null);
  const create = useMutation({
    mutationFn: () => {
      const encoded = JSON.stringify(input);
      if (key.current?.input !== encoded) key.current = { input: encoded, key: randomUUID() };
      return ebook
        ? ebookRequestsApi.create(
            { title: input.title, author: input.author, isbn: input.isbn, note: input.note },
            key.current.key,
            context,
          )
        : bookRequestsApi.create(input, key.current.key, context);
    },
    onSuccess: () => {
      setInput(emptyInput);
      key.current = null;
      toast.success(`${label} requested`);
      void qc.invalidateQueries({ queryKey: [cache] });
    },
    onError: (err) => toast.error(err.message),
  });
  const cancel = useMutation({
    mutationFn: (row: BookRequest) => api.cancel(row, context),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: [cache] });
    },
    onError: (err) => {
      toast.error(err.message);
      void qc.invalidateQueries({ queryKey: [cache] });
    },
  });
  const policy = capability.data;
  const filingEnabled = policy?.available && policy.enabled && policy.can_request;
  const canRequest = filingEnabled && (policy.unlimited || policy.remaining > 0);
  return (
    <section className="mx-auto max-w-4xl space-y-6 px-4 py-6 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold">{label} requests</h1>
        <p className="text-muted-foreground text-sm">
          Request a title for your organization. Approved requests wait until the {noun} is added.
        </p>
      </header>
      {capability.isError && <p role="alert">{capability.error.message}</p>}
      {policy && !filingEnabled && (
        <p role="status">
          {ebook && policy.available && !policy.enabled
            ? "Ebook requests are currently turned off."
            : `${label} requests are unavailable for this profile.`}
        </p>
      )}
      {filingEnabled && policy && (
        <form
          className="grid gap-4 rounded-xl border p-5 sm:grid-cols-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (canRequest && (!ebook || input.isbn.trim())) create.mutate();
          }}
        >
          <p className="text-muted-foreground text-sm sm:col-span-2">
            {policy.unlimited
              ? "No request limit"
              : `${policy.remaining} of ${policy.limit} requests left in ${policy.window_days} days`}
            .{" "}
            {policy.auto_approve
              ? "Requests are approved automatically."
              : "An admin will review your request."}
          </p>
          {[
            { key: "title", label: "Title", required: true, max: 500 },
            {
              key: "author",
              label: ebook ? "Author (optional)" : "Author",
              required: !ebook && !input.isbn && !input.asin,
              max: 500,
            },
            { key: "isbn", label: ebook ? "ISBN" : "ISBN (optional)", required: ebook, max: 32 },
            ...(!ebook
              ? [{ key: "asin", label: "ASIN (optional)", required: false, max: 10 }]
              : []),
          ].map((field) => (
            <label key={field.key} className="space-y-2 text-sm font-medium">
              {field.label}
              <Input
                value={input[field.key as keyof BookRequestInput]}
                required={field.required}
                maxLength={field.max}
                disabled={create.isPending}
                onChange={(event) => setInput({ ...input, [field.key]: event.target.value })}
              />
            </label>
          ))}
          <label className="space-y-2 text-sm font-medium sm:col-span-2">
            Note (optional)
            <textarea
              className="border-input focus-visible:border-ring focus-visible:ring-ring/50 min-h-24 w-full rounded-md border bg-transparent px-3 py-2 text-sm shadow-xs outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
              value={input.note}
              maxLength={2000}
              disabled={create.isPending}
              onChange={(event) => setInput({ ...input, note: event.target.value })}
            />
          </label>
          <p className="text-muted-foreground text-xs sm:col-span-2">
            {ebook
              ? "Provide the ISBN of the edition you want. Fulfilment is complete when the ebook is available in your library."
              : "ISBN or ASIN helps match the audiobook when it arrives. Without an identifier, an admin can mark your request fulfilled."}
          </p>
          {ebook && policy.fulfilment_backend === "unconfigured" && (
            <p role="status" className="text-muted-foreground text-sm sm:col-span-2">
              Ebook fulfillment is not configured. Approved requests will wait for setup.
            </p>
          )}
          <Button type="submit" disabled={!canRequest || create.isPending}>
            {create.isPending ? "Requesting…" : `Request ${noun}`}
          </Button>
        </form>
      )}
      <h2 className="text-xl font-semibold">Your {noun} requests</h2>
      {ebook && (
        <p className="text-muted-foreground text-sm">
          Cancellation is local to Bloem; the fulfilment provider may already be processing the
          ebook.
        </p>
      )}
      {mine.isLoading && <p role="status">Loading requests…</p>}
      {mine.isError && <p role="alert">{mine.error.message}</p>}
      {mine.data?.requests.length === 0 && (
        <p className="text-muted-foreground">You have no {noun} requests.</p>
      )}
      <ul className="divide-y rounded-xl border">
        {mine.data?.requests.map((row) => (
          <li key={row.id} className="flex flex-wrap items-center justify-between gap-4 p-4">
            <div className="min-w-0 space-y-1">
              <p className="font-medium">{row.title}</p>
              <p className="text-muted-foreground text-sm">{row.author}</p>
              {ebook && row.isbn && (
                <p className="text-muted-foreground text-xs">ISBN {row.isbn}</p>
              )}
              <p className="text-sm">{bookRequestState(row)}</p>
              {row.outcome_reason && (
                <p className="text-muted-foreground text-sm">{row.outcome_reason}</p>
              )}
            </div>
            {row.status === "completed" &&
            row.library_content_id &&
            (!ebook || row.fulfilment === "library") ? (
              <Button asChild size="sm" variant="outline">
                <Link to={`/item/${encodeURIComponent(row.library_content_id)}`}>Open {noun}</Link>
              </Button>
            ) : (
              row.outcome === "active" &&
              row.status !== "completed" && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={cancel.isPending}
                  onClick={() => cancel.mutate(row)}
                >
                  Cancel request
                </Button>
              )
            )}
          </li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={offset === 0 || mine.isFetching}
          onClick={() => setOffset(Math.max(0, offset - 100))}
        >
          Previous
        </Button>
        <Button
          variant="outline"
          disabled={mine.data?.requests.length !== 100 || mine.isFetching}
          onClick={() => setOffset(offset + 100)}
        >
          Next
        </Button>
      </div>
    </section>
  );
}
