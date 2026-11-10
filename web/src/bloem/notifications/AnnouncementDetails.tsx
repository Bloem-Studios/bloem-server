import type { AppNotification } from "@/api/types";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";

function severityLabel(bt: BloemTranslate, severity: AppNotification["severity"]): string {
  if (severity === "critical") return bt("bloem.notifications.announcement.severity.critical");
  if (severity === "warning") return bt("bloem.notifications.announcement.severity.warning");
  return bt("bloem.notifications.announcement.severity.info");
}

/** An announcement's whole message, wrapped rather than cut to one line, and how serious it is. */
export function AnnouncementDetails({ notification }: { notification: AppNotification }) {
  const bt = useBloemT();
  return (
    <>
      {notification.body ? (
        <div className="text-muted-foreground mt-0.5 text-sm break-words whitespace-pre-wrap">
          {notification.body}
        </div>
      ) : null}
      <p className="mt-2 text-xs font-medium">{severityLabel(bt, notification.severity)}</p>
    </>
  );
}
