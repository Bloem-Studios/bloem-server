import { AlertTriangle, CheckCircle2, Clock3 } from "lucide-react";

import { jobHeading, resultReasonLabel } from "@/components/admin/policy/policyVocabulary";
import type { PeopleBulkJob } from "@/hooks/queries/admin/organizationPeople";

export default function BulkJobResult({ job }: { job: PeopleBulkJob }) {
  const pending = job.status === "queued" || job.status === "running";
  return (
    <section className="surface-panel rounded-2xl p-5" aria-live="polite">
      <div className="flex items-start gap-3">
        {pending ? (
          <Clock3 className="text-muted-foreground mt-0.5 h-5 w-5" aria-hidden />
        ) : job.failed.length || job.skipped.length ? (
          <AlertTriangle className="mt-0.5 h-5 w-5 text-amber-600" aria-hidden />
        ) : (
          <CheckCircle2 className="mt-0.5 h-5 w-5 text-emerald-600" aria-hidden />
        )}
        <div className="min-w-0 flex-1">
          <h2 className="font-medium">{jobHeading(job)}</h2>
          <p className="text-muted-foreground mt-1 text-sm">
            {job.progress_current.toLocaleString()} of {job.progress_total.toLocaleString()} done
          </p>
          <div className="mt-3 flex flex-wrap gap-3 text-sm">
            <span>{job.succeeded.toLocaleString()} changed</span>
            <span>{job.skipped.length.toLocaleString()} skipped</span>
            <span>{job.failed.length.toLocaleString()} failed</span>
          </div>
          {job.skipped.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">Skipped</h3>
              <ul className="text-muted-foreground mt-1 space-y-1 text-sm">
                {job.skipped.map((record) => (
                  <li key={`skipped-${record.account_id}`}>
                    Account {record.account_id}: {resultReasonLabel(record.reason)}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {job.failed.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">Failed</h3>
              <ul className="text-destructive mt-1 space-y-1 text-sm">
                {job.failed.map((record) => (
                  <li key={`failed-${record.account_id}`}>
                    Account {record.account_id}: {resultReasonLabel(record.reason)}
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
