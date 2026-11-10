import { useBloemT } from "@/bloem/i18n";
import { AlertTriangle, CheckCircle2, Clock3 } from "lucide-react";

import { jobHeading, resultReasonLabel } from "@/components/admin/policy/policyVocabulary";
import type { PeopleBulkJob } from "@/hooks/queries/admin/organizationPeople";

export default function BulkJobResult({ job }: { job: PeopleBulkJob }) {
  const bt = useBloemT();
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
            {bt("bloem.admin.people.bulkResult.ofDone", {
              progressCurrent: job.progress_current.toLocaleString(),
              progressTotal: job.progress_total.toLocaleString(),
            })}
          </p>
          <div className="mt-3 flex flex-wrap gap-3 text-sm">
            <span>
              {bt("bloem.admin.people.bulkResult.changed", {
                succeeded: job.succeeded.toLocaleString(),
              })}
            </span>
            <span>
              {bt("bloem.admin.people.bulkResult.skipped", {
                skipped: job.skipped.length.toLocaleString(),
              })}
            </span>
            <span>
              {bt("bloem.admin.people.bulkResult.failed", {
                failed: job.failed.length.toLocaleString(),
              })}
            </span>
          </div>
          {job.skipped.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">
                {bt("bloem.admin.people.bulkResult.skipped2")}
              </h3>
              <ul className="text-muted-foreground mt-1 space-y-1 text-sm">
                {job.skipped.map((record) => (
                  <li key={`skipped-${record.account_id}`}>
                    {bt("bloem.admin.people.bulkResult.account", {
                      accountId: record.account_id,
                      resultReason: resultReasonLabel(record.reason),
                    })}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {job.failed.length > 0 ? (
            <div className="mt-4">
              <h3 className="text-sm font-medium">{bt("bloem.admin.people.bulkResult.failed2")}</h3>
              <ul className="text-destructive mt-1 space-y-1 text-sm">
                {job.failed.map((record) => (
                  <li key={`failed-${record.account_id}`}>
                    {bt("bloem.admin.people.bulkResult.account", {
                      accountId: record.account_id,
                      resultReason: resultReasonLabel(record.reason),
                    })}
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
