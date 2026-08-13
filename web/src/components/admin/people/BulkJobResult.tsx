import { AlertTriangle, CheckCircle2, Clock3 } from "lucide-react";

import type { PeopleBulkJob } from "@/hooks/queries/admin/organizationPeople";

const readableReason = (reason: string) => reason.replace(/_/g, " ");

export default function BulkJobResult({ job }: { job: PeopleBulkJob }) {
  const pending = job.status === "queued" || job.status === "running";
  return (
    <section className="surface-panel rounded-2xl p-5" aria-live="polite">
      <div className="flex items-start gap-3">
        {pending ? (
          <Clock3 className="text-muted-foreground mt-0.5 h-5 w-5" />
        ) : job.failed.length || job.skipped.length ? (
          <AlertTriangle className="mt-0.5 h-5 w-5 text-amber-500" />
        ) : (
          <CheckCircle2 className="mt-0.5 h-5 w-5 text-emerald-500" />
        )}
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold">Bulk job {job.status}</h2>
          <p className="text-muted-foreground mt-1 text-sm">
            {job.progress_current.toLocaleString()} of {job.progress_total.toLocaleString()}{" "}
            processed
          </p>
          <div className="mt-3 flex flex-wrap gap-3 text-sm">
            <span>{job.succeeded.toLocaleString()} succeeded</span>
            <span>{job.skipped.length.toLocaleString()} skipped</span>
            <span>{job.failed.length.toLocaleString()} failed</span>
          </div>
          {job.skipped.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">Skipped records</h3>
              <ul className="text-muted-foreground mt-1 space-y-1 text-sm">
                {job.skipped.map((record) => (
                  <li key={`skipped-${record.account_id}`}>
                    Account {record.account_id} — {readableReason(record.reason)}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {job.failed.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">Failed records</h3>
              <ul className="text-destructive mt-1 space-y-1 text-sm">
                {job.failed.map((record) => (
                  <li key={`failed-${record.account_id}`}>
                    Account {record.account_id} — {readableReason(record.reason)}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      </div>
    </section>
  );
}
