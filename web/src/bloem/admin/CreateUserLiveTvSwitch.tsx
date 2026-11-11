import { useId } from "react";

import { useBloemT } from "@/bloem/i18n";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { hasAssignedPermission, setAssignedPermission } from "@/lib/permissions";

/**
 * The create-account dialog's Live TV switch, beside Silo's permission
 * switches: same row shape, Bloem's own label.
 */
export function CreateUserLiveTvSwitch({
  permissions,
  setPermissions,
}: {
  permissions: string[];
  setPermissions: (update: (permissions: string[]) => string[]) => void;
}) {
  const bt = useBloemT();
  const liveTVId = useId();
  return (
    <div className="border-border flex items-center justify-between rounded-md border px-3 py-2">
      <div>
        <Label htmlFor={liveTVId}>{bt("bloem.admin.users.access.liveTv.label")}</Label>
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.users.access.liveTv.description")}
        </p>
      </div>
      <Switch
        id={liveTVId}
        checked={hasAssignedPermission(permissions, PERMISSION_WATCH_LIVE_TV)}
        onCheckedChange={(checked) =>
          setPermissions((current) =>
            setAssignedPermission(current, PERMISSION_WATCH_LIVE_TV, checked),
          )
        }
      />
    </div>
  );
}
