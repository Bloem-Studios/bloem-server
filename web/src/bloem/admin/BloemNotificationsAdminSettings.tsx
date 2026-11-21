import NotificationsAdminSettings from "@/pages/admin-settings/NotificationsAdminSettings";

import { BloemPushRelaySettings } from "./BloemPushRelaySettings";

/**
 * Silo's notification settings page, then the push relay for the Bloem Admin
 * app. The admin settings layout gets this page in place of Silo's
 * (bloem-preview-plugin.ts), so Silo's page stays as Silo wrote it.
 */
export default function BloemNotificationsAdminSettings() {
  return (
    <div className="flex h-full flex-col gap-5">
      <NotificationsAdminSettings />
      <BloemPushRelaySettings />
    </div>
  );
}
