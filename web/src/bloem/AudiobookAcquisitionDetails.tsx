import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  audiobookAcquisitionsApi,
  type AcquisitionDecisionRecord,
  type AudiobookAcquisition,
} from "@/api/bloemAudiobookAcquisitions";
import { AdminV2ClientError } from "@/api/adminV2Client";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
const readable = (value: string) => value.replaceAll("_", " ");
// A divider glyph between facts, the same in every language.
const DIVIDER = " · ";
function Decision({ record }: { record: AcquisitionDecisionRecord }) {
  const bt = useBloemT();
  const decision = record.decision;
  return (
    <article className="space-y-2 border-t pt-3 text-sm">
      <p className="font-medium">
        {bt("bloem.admin.bookRequests.decision.heading", {
          attempt: record.attempt,
          phase: readable(record.phase),
          outcome: readable(record.outcome),
        })}
      </p>
      {decision.detail && <p>{readable(decision.detail)}</p>}
      {decision.chosen && (
        <p>
          {bt("bloem.admin.bookRequests.decision.chosen", {
            title: decision.chosen.title,
            source: decision.chosen.source,
            size: decision.chosen.size_mb,
          })}
        </p>
      )}
      {decision.heuristic && (
        <p>
          {bt("bloem.admin.bookRequests.decision.heuristic", {
            score: Math.round(decision.heuristic.h * 100),
            reason: decision.heuristic.reason,
          })}
        </p>
      )}
      {decision.ai && (
        <div className="space-y-1">
          <p>
            {!decision.ai.used
              ? bt("bloem.admin.bookRequests.decision.aiNotUsed")
              : decision.ai.model
                ? bt("bloem.admin.bookRequests.decision.aiUsed", {
                    model: decision.ai.model,
                    confidence: Math.round(decision.ai.confidence * 100),
                  })
                : bt("bloem.admin.bookRequests.decision.aiAssessment", {
                    confidence: Math.round(decision.ai.confidence * 100),
                  })}
            {decision.ai.error ? DIVIDER + readable(decision.ai.error) : ""}
          </p>
          {decision.ai.reason && <p>{decision.ai.reason}</p>}
          {decision.ai.flags?.length > 0 && (
            <p>
              {bt("bloem.admin.bookRequests.decision.flags", {
                flags: decision.ai.flags.map(readable).join(DIVIDER),
              })}
            </p>
          )}
        </div>
      )}
      {decision.issues?.length ? (
        <p>
          {bt("bloem.admin.bookRequests.decision.issues", {
            issues: decision.issues.map(readable).join(DIVIDER),
          })}
        </p>
      ) : null}
      {decision.queries?.length ? (
        <p>
          {bt("bloem.admin.bookRequests.decision.searches", {
            queries: decision.queries.join(DIVIDER),
          })}
        </p>
      ) : null}
      {decision.sources?.length ? (
        <ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1">
          {decision.sources.map((source) => (
            <li key={source.name}>{source.name + DIVIDER + readable(source.status)}</li>
          ))}
        </ul>
      ) : null}
    </article>
  );
}
export function AudiobookAcquisitionDetails({ id }: { id: string }) {
  const bt = useBloemT();
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
        throw new Error(bt("bloem.admin.bookRequests.details.reloadBeforeAction"));
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
      setMessage(bt("bloem.admin.bookRequests.details.actionSaved"));
      void refresh();
      void qc.invalidateQueries({ queryKey: ["bloem-audiobook-acquisitions", scope.key] });
      void qc.invalidateQueries({ queryKey: ["bloem-admin-book-requests"] });
    },
    onError: (error) => {
      if (!scope.current()) return;
      setBlocked(true);
      setMessage(
        error instanceof AdminV2ClientError
          ? bt("bloem.admin.bookRequests.details.actionRefused", { error: error.message })
          : bt("bloem.admin.bookRequests.details.actionUnknown"),
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
      aria-label={bt("bloem.admin.bookRequests.details.title")}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">{bt("bloem.admin.bookRequests.details.title")}</h3>
        <Button
          size="sm"
          variant="outline"
          disabled={detail.isFetching || mutation.isPending}
          onClick={() => void refresh()}
        >
          {bt("bloem.admin.bookRequests.details.reload")}
        </Button>
      </div>
      {detail.isPending && <p role="status">{bt("bloem.admin.bookRequests.details.loading")}</p>}
      {detail.isError && <p role="alert">{bt("bloem.admin.bookRequests.details.loadFailed")}</p>}
      {message && <p role="status">{message}</p>}
      {row && (
        <>
          <dl className="grid gap-3 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-muted-foreground">
                {bt("bloem.admin.bookRequests.details.providerStatus")}
              </dt>
              <dd>
                {row.provider_status
                  ? readable(row.provider_status)
                  : bt("bloem.admin.bookRequests.notStarted")}
                {row.external_status ? DIVIDER + readable(row.external_status) : ""}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">
                {bt("bloem.admin.bookRequests.details.attempt")}
              </dt>
              <dd>
                {bt("bloem.admin.bookRequests.details.attemptSummary", {
                  attempt: row.attempt,
                  searchPasses: row.search_passes,
                  aiCalls: row.ai_calls,
                })}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">
                {bt("bloem.admin.bookRequests.details.connection")}
              </dt>
              <dd>{row.integration_name || bt("bloem.admin.bookRequests.details.notAssigned")}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">
                {bt("bloem.admin.bookRequests.details.failures")}
              </dt>
              <dd>{row.failure_count}</dd>
            </div>
            {row.next_attempt_at && (
              <div>
                <dt className="text-muted-foreground">
                  {bt("bloem.admin.bookRequests.details.nextCheck")}
                </dt>
                <dd>{new Date(row.next_attempt_at).toLocaleString()}</dd>
              </div>
            )}
            {row.provider_completed_at && (
              <div>
                <dt className="text-muted-foreground">
                  {bt("bloem.admin.bookRequests.details.downloadCompleted")}
                </dt>
                <dd>{new Date(row.provider_completed_at).toLocaleString()}</dd>
              </div>
            )}
          </dl>
          {row.stuck && (
            <p role="status">
              {bt("bloem.admin.bookRequests.details.needsAttention", {
                reason: row.stuck_reason
                  ? readable(row.stuck_reason)
                  : bt("bloem.admin.bookRequests.details.stuck"),
              })}
            </p>
          )}
          {row.request.outcome_reason && <p>{row.request.outcome_reason}</p>}
          {active && row.provider_status === "review" && row.review_kind === "selection" && (
            <div className="space-y-3">
              <h4 className="font-medium">
                {bt("bloem.admin.bookRequests.details.chooseRelease")}
              </h4>
              <p className="text-muted-foreground text-sm">
                {bt("bloem.admin.bookRequests.details.chooseReleaseHelp")}
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
                        {candidate.format
                          ? bt("bloem.admin.bookRequests.details.candidateSummaryWithFormat", {
                              source: candidate.source,
                              size: candidate.size_mb,
                              format: candidate.format,
                              heuristic: Math.round(candidate.h * 100),
                            })
                          : bt("bloem.admin.bookRequests.details.candidateSummary", {
                              source: candidate.source,
                              size: candidate.size_mb,
                              heuristic: Math.round(candidate.h * 100),
                            })}
                      </p>
                    </div>
                    <Button
                      size="sm"
                      disabled={busy}
                      aria-label={bt("bloem.admin.bookRequests.details.approveCandidateLabel", {
                        title: candidate.title,
                      })}
                      onClick={() =>
                        mutation.mutate({ row, action: "candidate", candidate: candidate.ref })
                      }
                    >
                      {bt("bloem.admin.bookRequests.details.approveCandidate")}
                    </Button>
                  </div>
                ))
              ) : (
                <p role="status">{bt("bloem.admin.bookRequests.details.noCandidates")}</p>
              )}
            </div>
          )}
          {active &&
            row.provider_status === "review" &&
            row.review_kind === "verification" &&
            (multiple ? (
              <p role="status">{bt("bloem.admin.bookRequests.details.multipleItems")}</p>
            ) : (
              <div className="space-y-2">
                <p className="text-muted-foreground text-sm">
                  {bt("bloem.admin.bookRequests.details.acceptHelp")}
                </p>
                <Button disabled={busy} onClick={() => mutation.mutate({ row, action: "accept" })}>
                  {bt("bloem.admin.bookRequests.details.acceptDownload")}
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
                {bt("bloem.admin.bookRequests.details.retry")}
              </Button>
            )}
          <h4 className="font-medium">{bt("bloem.admin.bookRequests.details.latestDecisions")}</h4>
          {records.length === 0 && (
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.bookRequests.details.noDecisions")}
            </p>
          )}
          {records.map((record) => (
            <Decision key={record.id} record={record} />
          ))}
        </>
      )}
    </section>
  );
}
