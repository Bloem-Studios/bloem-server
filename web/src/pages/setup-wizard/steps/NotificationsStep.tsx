import { useState, useMemo } from "react";
import type { FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "sonner";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { MobilePushPrivacyDisclosure } from "@/components/notifications/MobilePushPrivacyDisclosure";
import { useWizardContext } from "../WizardContext";

const APPLE_KEY = "notifications.apple_push_delivery_enabled";
const ANDROID_KEY = "notifications.android_push_delivery_enabled";
const KEYS = [APPLE_KEY, ANDROID_KEY];

export function NotificationsStep() {
  const { markDone } = useWizardContext();
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const [submitting, setSubmitting] = useState(false);

  // The server default is on; an unset value renders as enabled so the switch
  // reflects what will actually happen if the admin just continues.
  const enabled = form.getValue(APPLE_KEY) !== "false" || form.getValue(ANDROID_KEY) !== "false";

  function setEnabled(value: boolean) {
    form.setValue(APPLE_KEY, String(value));
    form.setValue(ANDROID_KEY, String(value));
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (form.dirtyCount === 0) {
      markDone("notifications");
      return;
    }
    setSubmitting(true);
    try {
      await form.save();
      markDone("notifications");
      toast.success("Push notification settings saved");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to save push notification settings");
    } finally {
      setSubmitting(false);
    }
  }

  if (form.isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-14 w-full rounded-xl" />
        <Skeleton className="h-40 w-full rounded-xl" />
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div className="border-foreground/[0.07] bg-foreground/[0.03] flex items-center justify-between rounded-xl border px-4 py-3.5">
        <div>
          <Label htmlFor="push-delivery-enabled" className="text-sm font-medium">
            Mobile push notifications
          </Label>
          <p className="text-muted-foreground/70 mt-0.5 text-xs">
            Deliver to the iOS and Android apps through Silo's push relay. Enabled by default.
          </p>
        </div>
        <Switch id="push-delivery-enabled" checked={enabled} onCheckedChange={setEnabled} />
      </div>

      <div className="border-foreground/[0.07] bg-foreground/[0.03] rounded-xl border px-4">
        <MobilePushPrivacyDisclosure />
      </div>

      <p className="text-muted-foreground/70 text-xs">
        You can change this any time under Admin settings → Notifications.
      </p>

      <div className="flex gap-3 pt-2">
        <Button type="submit" disabled={submitting || form.isSaving}>
          {submitting || form.isSaving ? "Saving..." : "Continue"}
        </Button>
      </div>
    </form>
  );
}
