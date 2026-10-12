import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  audiobookAcquisitionsApi,
  type AcquisitionDecisionRecord,
  type AudiobookAcquisition,
} from "@/api/bloemAudiobookAcquisitions";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { Button } from "@/components/ui/button";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
const readable = (value: string) => value.replaceAll("_", " ");
function Decision({ record }: { record: AcquisitionDecisionRecord }) {
  const decision = record.decision;
  return (
    <article className="space-y-2 border-t pt-3 text-sm">
      <p className="font-medium">
        Attempt {record.attempt} · {readable(record.phase)} · {readable(record.outcome)}
      </p>
      {decision.detail && <p>{readable(decision.detail)}</p>}
      {decision.chosen && (
        <p>
          Chosen release: {decision.chosen.title} · {decision.chosen.source} ·{" "}
          {decision.chosen.size_mb} MB
        </p>
      )}
      {decision.heuristic && (
        <p>
          Heuristic {Math.round(decision.heuristic.h * 100)}% · {decision.heuristic.reason}
        </p>
      )}
      {decision.ai && (
        <div className="space-y-1">
          <p>
            AI{" "}
            {decision.ai.used
              ? `${decision.ai.model || "assessment"} · ${Math.round(decision.ai.confidence * 100)}% confidence`
              : "not used"}
            {decision.ai.error ? ` · ${readable(decision.ai.error)}` : ""}
          </p>
          {decision.ai.reason && <p>{decision.ai.reason}</p>}
          {decision.ai.flags?.length > 0 && (
            <p>Flags: {decision.ai.flags.map(readable).join(" · ")}</p>
          )}
        </div>
      )}
      {decision.issues?.length ? <p>Issues: {decision.issues.map(readable).join(" · ")}</p> : null}
      {decision.queries?.length ? <p>Searches: {decision.queries.join(" · ")}</p> : null}
      {decision.sources?.length ? (
        <ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1">
          {decision.sources.map((source) => (
            <li key={source.name}>
              {source.name} · {readable(source.status)}
            </li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}
export function AudiobookAcquisitionDetails({ id }: { id: string }) {
  const scope = useAudiobookAdminScope();
  const qc = useQueryClient();
  const queryKey = ["bloem-audiobook-acquisition-detail", scope.key, id];
  const detail = useQuery({
    queryKey,
    queryFn: ({ signal }) => audiobookAcquisitionsApi.detail(id, scope.context, signal),
    enabled: scope.allowed,
    retry: false,
  });
  const [blocked, setBlocked] = useState(false);
  const [message, setMessage] = useState("");
  const mutation = useMutation({
    retry: false,
    mutationFn: ({
      row,
      action,
      candidate,
    }: {
      row: AudiobookAcquisition;
      action: "candidate" | "accept" | "retry";
      candidate?: string;
    }) => {
      if (!scope.current() || blocked)
        throw new Error("Reload this acquisition before another action.");
      if (action === "candidate" && candidate)
        return audiobookAcquisitionsApi.approveCandidate(
          row.request.id,
          row.request.revision,
          candidate,
          scope.context,
        );
      if (action === "accept")
        return audiobookAcquisitionsApi.acceptDownload(
          row.request.id,
          row.request.revision,
          scope.context,
        );
      return audiobookAcquisitionsApi.retry(row.request.id, row.request.revision, scope.context);
    },
    onSuccess: () => {
      if (!scope.current()) return;
      setBlocked(true);
      setMessage("Action saved. Reloading acquisition…");
      void refresh();
      void qc.invalidateQueries({ queryKey: ["bloem-audiobook-acquisitions", scope.key] });
      void qc.invalidateQueries({ queryKey: ["bloem-admin-book-requests"] });
    },
    onError: (error) => {
      if (!scope.current()) return;
      setBlocked(true);
      setMessage(
        error instanceof AdminV2ClientError
          ? `${error.message} Reload the acquisition before another action.`
          : "The action outcome is unknown. Reload the acquisition before another action.",
      );
    },
  });
  async function refresh() {
    const result = await detail.refetch();
    if (!scope.current() || result.isError) return;
    setBlocked(false);
    setMessage("");
  }
  const row = detail.data;
  const records = [...(row?.decisions ?? [])].sort((a, b) => b.id - a.id);
  const current = records.filter((record) => record.attempt === row?.attempt);
  const held = current.find((record) => record.phase === "select" && record.outcome === "held");
  const verification = current.find((record) => record.phase === "verify");
  const multiple = verification?.decision.issues?.includes("multiple_items") ?? false;
  const active = row?.request.status === "approved" && row.request.outcome === "active";
  const busy =
    mutation.isPending || detail.isFetching || detail.isError || scope.switching || blocked;
  return (
    <section
      className="bg-muted/20 space-y-4 rounded-lg border p-4"
      aria-label="Acquisition details"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">Acquisition details</h3>
        <Button
          size="sm"
          variant="outline"
          disabled={detail.isFetching || mutation.isPending}
          onClick={() => void refresh()}
        >
          Reload acquisition
        </Button>
      </div>
      {detail.isPending && <p role="status">Loading acquisition details…</p>}
      {detail.isError && <p role="alert">Unable to load this acquisition. Reload to try again.</p>}
      {message && <p role="status">{message}</p>}
      {row && (
        <>
          <dl className="grid gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-muted-foreground">Provider status</dt>
              <dd>
                {readable(row.provider_status || "Not started")}
                {row.external_status ? ` · ${readable(row.external_status)}` : ""}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Attempt</dt>
              <dd>
                {row.attempt} · {row.search_passes} search passes · {row.ai_calls} AI calls
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Connection</dt>
              <dd>{row.integration_name || "Not assigned"}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Failures</dt>
              <dd>{row.failure_count}</dd>
            </div>
            {row.next_attempt_at && (
              <div>
                <dt className="text-muted-foreground">Next check</dt>
                <dd>{new Date(row.next_attempt_at).toLocaleString()}</dd>
              </div>
            )}
            {row.provider_completed_at && (
              <div>
                <dt className="text-muted-foreground">Download completed</dt>
                <dd>{new Date(row.provider_completed_at).toLocaleString()}</dd>
              </div>
            )}
          </dl>
          {row.stuck && (
            <p role="status">Needs attention: {readable(row.stuck_reason || "stuck")}</p>
          )}
          {row.request.outcome_reason && <p>{row.request.outcome_reason}</p>}
          {active && row.provider_status === "review" && row.review_kind === "selection" && (
            <div className="space-y-3">
              <h4 className="font-medium">Choose a release</h4>
              <p className="text-muted-foreground text-sm">
                Approve the exact release below to start a new acquisition attempt.
              </p>
              {held?.decision.candidates?.length ? (
                held.decision.candidates.map((candidate) => (
                  <div
                    key={candidate.ref}
                    className="flex flex-col justify-between gap-3 rounded-md border p-3 sm:flex-row sm:items-center"
                  >
                    <div className="min-w-0 text-sm">
                      <p className="font-medium break-words">{candidate.title}</p>
                      <p className="text-muted-foreground">
                        {candidate.source} · {candidate.size_mb} MB
                        {candidate.format ? ` · ${candidate.format}` : ""} ·{" "}
                        {Math.round(candidate.h * 100)}% heuristic
                      </p>
                    </div>
                    <Button
                      size="sm"
                      disabled={busy}
                      aria-label={`Approve candidate ${candidate.title}`}
                      onClick={() =>
                        mutation.mutate({ row, action: "candidate", candidate: candidate.ref })
                      }
                    >
                      Approve candidate
                    </Button>
                  </div>
                ))
              ) : (
                <p role="status">
                  No current held candidates are available. Reload before choosing a release.
                </p>
              )}
            </div>
          )}
          {active &&
            row.provider_status === "review" &&
            row.review_kind === "verification" &&
            (multiple ? (
              <p role="status">
                Multiple library items were found. Inspect them and use the existing manual
                fulfillment action; this download cannot be accepted here.
              </p>
            ) : (
              <div className="space-y-2">
                <p className="text-muted-foreground text-sm">
                  Accepting this audio clears the verification review. Fulfillment still waits for
                  library availability.
                </p>
                <Button disabled={busy} onClick={() => mutation.mutate({ row, action: "accept" })}>
                  Accept downloaded audio
                </Button>
              </div>
            ))}
          {active &&
            (row.provider_status === "failed" ||
              row.provider_status === "review" ||
              row.stuck ||
              ["failed", "review", "retrying"].includes(row.request.fulfilment)) && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => mutation.mutate({ row, action: "retry" })}
              >
                Retry acquisition
              </Button>
            )}
          <h4 className="font-medium">Latest decisions</h4>
          {records.length === 0 && (
            <p className="text-muted-foreground text-sm">No decisions have been recorded yet.</p>
          )}
          {records.map((record) => (
            <Decision key={record.id} record={record} />
          ))}
        </>
      )}
    </section>
  );
}
