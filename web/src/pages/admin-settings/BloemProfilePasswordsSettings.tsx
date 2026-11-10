import { useQueryClient } from "@tanstack/react-query";

import { SaveBar } from "@/components/SaveBar";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useSettingsForm } from "@/hooks/useSettingsForm";

import { FieldGroup } from "./FieldGroup";
import { SettingField } from "./SettingField";

export const PROFILE_PASSWORDS_KEY = "bloem.auth.profile_passwords_enabled";
const KEYS = [PROFILE_PASSWORDS_KEY];

/**
 * Settings → Profile passwords (Bloem): whether household profiles may have
 * their own email and password. The public capability probe advertises
 * profile credential management only while this is on, so a save refreshes it
 * and the household credential section follows without a reload.
 */
export default function BloemProfilePasswordsSettings() {
  const form = useSettingsForm({ keys: KEYS });
  const queryClient = useQueryClient();

  async function save() {
    await form.save();
    await queryClient.invalidateQueries({ queryKey: ["bloem-capabilities"] });
  }

  if (form.isPending || form.isLoading)
    return (
      <div className="space-y-6" role="status" aria-label="Loading settings">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-10 w-full" />
        <span className="sr-only">Loading settings</span>
      </div>
    );

  if (form.loadError && !form.loaded)
    return (
      <div className="space-y-4">
        <SettingsPageHeader title="Profile passwords" />
        <p role="alert">Profile password settings could not be loaded.</p>
        <Button variant="outline" onClick={() => void form.retryLoad()}>
          Retry
        </Button>
      </div>
    );

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader title="Profile passwords" />

      <div className="flex-1 space-y-5">
        <FieldGroup label="Profile passwords" dirty={form.isDirty(PROFILE_PASSWORDS_KEY)}>
          <SettingField
            label="Profile passwords"
            type="toggle"
            description="Let a household give each profile its own email and password to sign in with. Off by default. Turning it off stops profile passwords from signing in; saved passwords are kept, and profiles already signed in stay signed in."
            dirty={form.isDirty(PROFILE_PASSWORDS_KEY)}
            value={form.getValue(PROFILE_PASSWORDS_KEY) || "false"}
            onChange={(value) => form.setValue(PROFILE_PASSWORDS_KEY, value)}
          />
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
      />
    </div>
  );
}
