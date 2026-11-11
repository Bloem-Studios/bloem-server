import { Link } from "react-router";

import type { BloemSearchMirrorState, BloemSearchMirrorStatus } from "@/api/bloemSearchMirror";
import { useBloemT, type BloemMessageKey, type BloemTranslate } from "@/bloem/i18n";
import { SettingsSubheading } from "@/components/settings/SettingsSubheading";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { useSearchMirrorStatus } from "@/hooks/queries/admin/searchMirror";
import { formatLocale } from "@/lib/appLocale";
import { formatDateTime } from "@/lib/datetime";
import { SettingFieldStatus } from "@/pages/admin-settings/SettingField";

const MESSAGE = "bloem.admin.searchMirror.";

/** The most rows waiting that the server counts; past it the count is "more than". */
const PENDING_EVENTS_CAP = 1_000_000;

const STATE_MESSAGES = {
  off: `${MESSAGE}state.off`,
  following: `${MESSAGE}state.following`,
  copying: `${MESSAGE}state.copying`,
  waiting: `${MESSAGE}state.waiting`,
  unreachable: `${MESSAGE}state.unreachable`,
  needs_full_copy: `${MESSAGE}state.needsFullCopy`,
  misconfigured: `${MESSAGE}state.misconfigured`,
  failing: `${MESSAGE}state.failing`,
  settling: `${MESSAGE}state.settling`,
} as const satisfies Record<BloemSearchMirrorState, BloemMessageKey>;

const WARNING_MESSAGES: Record<string, BloemMessageKey> = {
  configured_on_one_copy_only: `${MESSAGE}warning.configuredOnOneCopyOnly`,
  settling_overdue: `${MESSAGE}warning.settlingOverdue`,
  stood_down: `${MESSAGE}warning.stoodDown`,
};

/**
 * The search mirror's state, read-only, on the Search settings page. It shows
 * only to a Platform administrator, and only when the mirror is configured or
 * this database holds mirror state; otherwise it renders nothing.
 *
 * The context check comes first and apart from the query, so outside a
 * Platform context nothing here asks the server (or needs a query client).
 */
export function SearchMirrorStatus() {
  const platform = useOptionalAdminContext()?.active?.scope === "platform";
  return platform ? <SearchMirrorSection /> : null;
}

function SearchMirrorSection() {
  const bt = useBloemT();
  const { data: status, isLoading, isError } = useSearchMirrorStatus();
  if (isLoading || isError || !status) return null;
  if (!status.configured && !status.state_exists) return null;

  return (
    <>
      <SettingsSubheading>{bt("bloem.admin.searchMirror.heading")}</SettingsSubheading>
      <div className="divide-border divide-y">
        <MirrorRows status={status} bt={bt} />
        {status.warnings.map((code) => (
          <div key={code} className="py-3">
            <SettingFieldStatus tone="warn">{warningText(bt, code)}</SettingFieldStatus>
          </div>
        ))}
        <div className="flex flex-wrap gap-2 py-3">
          <Button asChild size="sm" variant="outline">
            <Link to="/admin/tasks/rebuild_catalog_search_mirror">
              {bt("bloem.admin.searchMirror.link.fullCopy")}
            </Link>
          </Button>
          <Button asChild size="sm" variant="ghost">
            <Link to="/admin/tasks/sync_catalog_search_mirror">
              {bt("bloem.admin.searchMirror.link.sync")}
            </Link>
          </Button>
        </div>
      </div>
    </>
  );
}

function MirrorRows({ status, bt }: { status: BloemSearchMirrorStatus; bt: BloemTranslate }) {
  const notBuilt = bt("bloem.admin.searchMirror.value.notBuilt");
  const lag = status.lag_seconds === null ? undefined : durationText(bt, status.lag_seconds);
  const errorSince = dateText(status.error_since);
  return (
    <>
      <Row
        label={bt("bloem.admin.searchMirror.row.state")}
        value={stateText(bt, status.state)}
        badge={status.reason}
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.upToDateAsOf")}
        value={dateText(status.up_to_date_as_of) || bt("bloem.admin.searchMirror.value.notYet")}
        badge={
          lag === undefined
            ? undefined
            : bt("bloem.admin.searchMirror.lagBehind", { duration: lag })
        }
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.rowsWaiting")}
        value={rowsWaitingText(bt, status)}
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.mirrorIndex")}
        value={status.mirror_index || notBuilt}
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.mainIndex")}
        value={status.main_index || notBuilt}
      />
      {status.copy && (
        <Row
          label={bt("bloem.admin.searchMirror.row.copyProgress")}
          value={bt("bloem.admin.searchMirror.copy.percent", { ratio: clamp01(status.copy.ratio) })}
          badge={bt("bloem.admin.searchMirror.copy.items", {
            done: status.copy.done,
            total: status.copy.total,
          })}
        />
      )}
      <Row
        label={bt("bloem.admin.searchMirror.row.lastFullCopy")}
        value={dateText(status.last_full_copy_at) || bt("bloem.admin.searchMirror.value.never")}
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.lastError")}
        value={status.last_error || bt("bloem.admin.searchMirror.value.none")}
        badge={
          status.last_error && errorSince
            ? bt("bloem.admin.searchMirror.errorSince", { when: errorSince })
            : undefined
        }
      />
      <Row
        label={bt("bloem.admin.searchMirror.row.deadLetters")}
        value={status.dead_letters.toLocaleString(formatLocale())}
      />
      {status.max_lag_seconds > 0 && (
        <Row
          label={bt("bloem.admin.searchMirror.row.lagCap")}
          value={durationText(bt, status.max_lag_seconds)}
        />
      )}
    </>
  );
}

function Row({ label, value, badge }: { label: string; value: string; badge?: string }) {
  return (
    <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
      <span className="text-sm font-medium">{label}</span>
      <span className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-2 text-sm">
        <span className="max-w-full text-end break-words">{value}</span>
        {badge ? <Badge variant="outline">{badge}</Badge> : null}
      </span>
    </div>
  );
}

/** The state's name; a state a newer server adds shows as its code. */
function stateText(bt: BloemTranslate, state: string): string {
  const message = (STATE_MESSAGES as Record<string, BloemMessageKey | undefined>)[state];
  return message ? bt(message) : state;
}

/** A warning's text; a code this version does not know shows as the code. */
function warningText(bt: BloemTranslate, code: string): string {
  const message = WARNING_MESSAGES[code];
  return message ? bt(message) : bt("bloem.admin.searchMirror.warning.unknown", { code });
}

function rowsWaitingText(bt: BloemTranslate, status: BloemSearchMirrorStatus): string {
  if (status.pending_events_capped) {
    return bt("bloem.admin.searchMirror.rowsWaiting.capped", { count: PENDING_EVENTS_CAP });
  }
  if (status.pending_events === null) return bt("bloem.admin.searchMirror.value.unknown");
  return bt("bloem.admin.searchMirror.rowsWaiting.count", { count: status.pending_events });
}

/** A length of time in the largest unit that keeps the number readable. */
function durationText(bt: BloemTranslate, totalSeconds: number): string {
  const seconds = Math.max(0, Math.round(totalSeconds));
  if (seconds < 60) return bt("bloem.admin.searchMirror.duration.seconds", { count: seconds });
  const minutes = Math.round(seconds / 60);
  if (minutes < 120) return bt("bloem.admin.searchMirror.duration.minutes", { count: minutes });
  const hours = Math.round(seconds / 3600);
  if (hours < 48) return bt("bloem.admin.searchMirror.duration.hours", { count: hours });
  return bt("bloem.admin.searchMirror.duration.days", { count: Math.round(seconds / 86400) });
}

/** A time in the viewer's date format; empty when there is none or it does not parse. */
function dateText(value: string | null): string {
  return value ? formatDateTime(value) : "";
}

function clamp01(value: number): number {
  return Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0;
}
