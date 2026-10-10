import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { toast } from "sonner";
import Requests from "@/pages/Requests";
import { useAuth } from "@/hooks/useAuth";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { captureProfileRequestContext } from "@/api/client";
import { nativeApi } from "@/api/bloemClient";
import {
  bookRequestsApi,
  bookRequestState,
  type BookRequest,
  type BookRequestInput,
} from "@/api/bloemBookRequests";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { randomUUID } from "@/lib/uuid";
import { RequestMediaTabs } from "./RequestMediaTabs";

export default function BloemRequests() {
  const [params] = useSearchParams();
  const capabilities = useQuery({
    queryKey: ["bloem-capabilities"],
    queryFn: () => nativeApi<{ feature_tokens: string[] }>("/capabilities"),
  });
  const supported = capabilities.data?.feature_tokens.includes("audiobook_requests_v1");
  return (
    <>
      {supported && <RequestMediaTabs />}
      {params.get("kind") === "audiobook" && supported ? <AudiobookRequests /> : <Requests />}
    </>
  );
}

const emptyInput: BookRequestInput = { title: "", author: "", isbn: "", asin: "", note: "" };
function AudiobookRequests() {
  useDocumentTitle("Audiobook requests");
  const { user, profile } = useAuth();
  const qc = useQueryClient();
  const context = captureProfileRequestContext();
  const scope = ["bloem-book-requests", user?.id, profile?.id, context?.generation] as const;
  const capability = useQuery({
    queryKey: [...scope, "capability"],
    queryFn: () => bookRequestsApi.capability(context),
    enabled: !!context,
  });
  const [offset, setOffset] = useState(0);
  const mine = useQuery({
    queryKey: [...scope, "mine", offset],
    queryFn: () => bookRequestsApi.mine(context, offset),
    enabled: !!context,
  });
  const [input, setInput] = useState<BookRequestInput>(emptyInput);
  const key = useRef<{ input: string; key: string } | null>(null);
  const create = useMutation({
    mutationFn: () => {
      const encoded = JSON.stringify(input);
      if (key.current?.input !== encoded) key.current = { input: encoded, key: randomUUID() };
      return bookRequestsApi.create(input, key.current.key, context);
    },
    onSuccess: () => {
      setInput(emptyInput);
      key.current = null;
      toast.success("Audiobook requested");
      void qc.invalidateQueries({ queryKey: ["bloem-book-requests"] });
    },
    onError: (err) => toast.error(err.message),
  });
  const cancel = useMutation({
    mutationFn: (row: BookRequest) => bookRequestsApi.cancel(row, context),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["bloem-book-requests"] });
    },
    onError: (err) => toast.error(err.message),
  });
  const policy = capability.data;
  const canRequest = policy?.can_request && (policy.unlimited || policy.remaining > 0);
  return (
    <section className="mx-auto max-w-4xl space-y-6 px-4 py-6 sm:px-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold">Audiobook requests</h1>
        <p className="text-muted-foreground text-sm">
          Request a title for your organization. Approved requests wait until the audiobook is
          added.
        </p>
      </header>
      {capability.isError && <p role="alert">{capability.error.message}</p>}
      {policy && !policy.can_request && (
        <p role="status">Audiobook requests are unavailable for this profile.</p>
      )}
      {policy?.can_request && (
        <form
          className="grid gap-4 rounded-xl border p-5 sm:grid-cols-2"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate();
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
            { key: "author", label: "Author", required: !input.isbn && !input.asin, max: 500 },
            { key: "isbn", label: "ISBN (optional)", required: false, max: 32 },
            { key: "asin", label: "ASIN (optional)", required: false, max: 10 },
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
            <Textarea
              value={input.note}
              maxLength={2000}
              disabled={create.isPending}
              onChange={(event) => setInput({ ...input, note: event.target.value })}
            />
          </label>
          <p className="text-muted-foreground text-xs sm:col-span-2">
            ISBN or ASIN helps match the audiobook when it arrives. Without an identifier, an admin
            can mark your request fulfilled.
          </p>
          <Button type="submit" disabled={!canRequest || create.isPending}>
            {create.isPending ? "Requesting…" : "Request audiobook"}
          </Button>
        </form>
      )}
      <h2 className="text-xl font-semibold">Your audiobook requests</h2>
      {mine.isLoading && <p role="status">Loading requests…</p>}
      {mine.isError && <p role="alert">{mine.error.message}</p>}
      {mine.data?.requests.length === 0 && (
        <p className="text-muted-foreground">You have no audiobook requests.</p>
      )}
      <ul className="divide-y rounded-xl border">
        {mine.data?.requests.map((row) => (
          <li key={row.id} className="flex flex-wrap items-center justify-between gap-4 p-4">
            <div className="min-w-0 space-y-1">
              <p className="font-medium">{row.title}</p>
              <p className="text-muted-foreground text-sm">{row.author}</p>
              <p className="text-sm">{bookRequestState(row)}</p>
              {row.outcome_reason && (
                <p className="text-muted-foreground text-sm">{row.outcome_reason}</p>
              )}
            </div>
            {row.status === "completed" && row.library_content_id ? (
              <Button asChild size="sm" variant="outline">
                <Link to={`/item/${encodeURIComponent(row.library_content_id)}`}>
                  Open audiobook
                </Link>
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
