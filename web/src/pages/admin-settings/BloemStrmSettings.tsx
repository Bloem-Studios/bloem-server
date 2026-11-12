import { useBloemT } from "@/bloem/i18n";

import { SaveBar } from "@/components/SaveBar";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useSettingsForm } from "@/hooks/useSettingsForm";

import { FieldGroup } from "./FieldGroup";
import { SettingField } from "./SettingField";

export const STRM_PRIVATE_ADDRESSES_KEY = "bloem.strm.allow_private_addresses";
const KEYS = [STRM_PRIVATE_ADDRESSES_KEY];

/**
 * Settings → .strm files (Bloem): whether the web addresses in `.strm` files
 * may point at the server's own network. Off by default; the server reads the
 * setting again within seconds, at the next connection.
 */
export default function BloemStrmSettings() {
  const bt = useBloemT();
  const form = useSettingsForm({ keys: KEYS });

  if (form.isPending || form.isLoading)
    return (
      <div className="space-y-6" role="status" aria-label={bt("bloem.admin.strm.loading")}>
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-10 w-full" />
        <span className="sr-only">{bt("bloem.admin.strm.loading")}</span>
      </div>
    );

  if (form.loadError && !form.loaded)
    return (
      <div className="space-y-4">
        <SettingsPageHeader title={bt("bloem.admin.strm.title")} />
        <p role="alert">{bt("bloem.admin.strm.loadFailed")}</p>
        <Button variant="outline" onClick={() => void form.retryLoad()}>
          {bt("bloem.admin.strm.retry")}
        </Button>
      </div>
    );

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader title={bt("bloem.admin.strm.title")} />

      <div className="flex-1 space-y-5">
        <FieldGroup
          label={bt("bloem.admin.strm.title")}
          dirty={form.isDirty(STRM_PRIVATE_ADDRESSES_KEY)}
        >
          <SettingField
            label={bt("bloem.admin.strm.privateAddresses")}
            type="toggle"
            description={bt("bloem.admin.strm.privateAddressesDescription")}
            dirty={form.isDirty(STRM_PRIVATE_ADDRESSES_KEY)}
            value={form.getValue(STRM_PRIVATE_ADDRESSES_KEY) || "false"}
            onChange={(value) => form.setValue(STRM_PRIVATE_ADDRESSES_KEY, value)}
          />
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
      />
    </div>
  );
}
