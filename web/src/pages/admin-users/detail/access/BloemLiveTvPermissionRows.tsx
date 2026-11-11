import { Lock } from "lucide-react";

import type { AccessGroup, AdminUser } from "@/api/types";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/i18n";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { hasAssignedPermission, setAssignedPermission } from "@/lib/permissions";
import { formatAllowed, permissionLock } from "@/pages/admin-users/detail/access/policySources";
import { KeyValueRow } from "@/pages/admin-users/detail/ui";

/**
 * The Live TV permission's row id in the account card's changed rows (the
 * card draft adapter adds it; the card names it through Bloem's catalog).
 */
export const LIVE_TV_ROW = "bloem.admin.users.access.liveTv.label";

/**
 * The account library card's Live TV switch, beside Silo's permission
 * switches: the same draft, group lock and wording, Bloem's own label.
 */
export function LiveTvPermissionEditRow({
  account,
  groups,
  permissions,
  changed,
  idPrefix,
  setPermissions,
}: {
  account: AdminUser;
  groups: AccessGroup[];
  permissions: string[];
  changed: ReadonlySet<string>;
  idPrefix: string;
  setPermissions: (update: (permissions: string[]) => string[]) => void;
}) {
  const t = useT();
  const bt = useBloemT();
  const lock = permissionLock(account, groups, PERMISSION_WATCH_LIVE_TV);
  const assigned = hasAssignedPermission(permissions, PERMISSION_WATCH_LIVE_TV);
  const switchId = `${idPrefix}-${PERMISSION_WATCH_LIVE_TV}`;
  let description = bt("bloem.admin.users.access.liveTv.description");
  if (lock.locked && assigned) {
    description = t("web.admin.users.detail.access.library.assignedButBlocked", {
      group: lock.groupName ?? "",
    });
  } else if (!lock.locked && lock.groupName) {
    description = t("web.admin.users.detail.access.library.groupAllows", {
      group: lock.groupName,
    });
  }
  const set = (on: boolean) =>
    setPermissions((current) => setAssignedPermission(current, PERMISSION_WATCH_LIVE_TV, on));
  return (
    <KeyValueRow
      label={<Label htmlFor={switchId}>{bt("bloem.admin.users.access.liveTv.label")}</Label>}
      description={description}
      changed={changed.has(LIVE_TV_ROW)}
      value={
        <span className="flex items-center gap-2">
          {lock.locked ? (
            <span className="text-muted-foreground inline-flex items-center gap-1 text-xs">
              <Lock aria-hidden="true" className="size-3" />
              {bt("bloem.admin.users.access.liveTv.groupDisallows", {
                group: lock.groupName ?? "",
              })}
            </span>
          ) : null}
          {lock.locked && assigned ? (
            <Button type="button" variant="ghost" size="sm" onClick={() => set(false)}>
              {t("web.admin.users.detail.access.library.removeFromAccount")}
            </Button>
          ) : null}
          <Switch
            id={switchId}
            checked={assigned && !lock.locked}
            disabled={lock.locked}
            onCheckedChange={set}
          />
        </span>
      }
    />
  );
}

/** The Live TV permission in the card's read view. */
export function LiveTvPermissionRow({
  user,
  groups,
  effectivePermissions,
}: {
  user: AdminUser;
  groups: AccessGroup[];
  effectivePermissions: string[];
}) {
  const t = useT();
  const bt = useBloemT();
  const lock = permissionLock(user, groups, PERMISSION_WATCH_LIVE_TV);
  const blocked = lock.locked && hasAssignedPermission(user.permissions, PERMISSION_WATCH_LIVE_TV);
  return (
    <KeyValueRow
      label={bt("bloem.admin.users.access.liveTv.label")}
      description={
        blocked
          ? t("web.admin.users.detail.access.library.blockedByGroup", {
              group: lock.groupName ?? "",
            })
          : bt("bloem.admin.users.access.liveTv.description")
      }
      value={formatAllowed(hasAssignedPermission(effectivePermissions, PERMISSION_WATCH_LIVE_TV))}
    />
  );
}
