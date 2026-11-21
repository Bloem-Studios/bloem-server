import { useState } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { adminV2Api, captureAdminRequestContext } from "@/api/adminV2Client";
import { useBloemT } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";
import { formatDateTime } from "@/lib/datetime";
import { FieldGroup } from "@/pages/admin-settings/FieldGroup";

/**
 * The answer of POST /api/bloem/v2/admin/platform/notifications/push/relay/register
 * (handlers.BloemPushRelayRegistration). It never carries the credential.
 */
export interface BloemPushRelayRegistration {
  relay_url: string;
  deployment_id: string;
  key_prefix: string;
  api_key_configured: boolean;
  relay_request_id?: string;
  apns_topics?: string[];
  expires_at: string;
}

/** The platform route, relative to the native admin prefix. */
export const BLOEM_PUSH_RELAY_REGISTER = "/platform/notifications/push/relay/register";

/**
 * Registers the server with the push relay that serves the Bloem Admin app,
 * on the notification settings page below Silo's settings. Silo's mobile push
 * card manages Silo's relay, for Silo's apps; this is the only way back after
 * Bloem's relay turned the server off (docs/architecture/bloem-push-relays.md).
 * It shows only in the Platform context, the route's own requirement.
 */
export function BloemPushRelaySettings() {
  const bt = useBloemT();
  // Optional: Silo's own tests render the settings page without the provider.
  const active = useOptionalAdminContext()?.active ?? null;
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<BloemPushRelayRegistration | null>(null);
  if (active?.scope !== "platform") return null;

  const register = async () => {
    if (pending) return;
    setPending(true);
    try {
      const registered = await adminV2Api<BloemPushRelayRegistration>(
        BLOEM_PUSH_RELAY_REGISTER,
        { method: "POST" },
        "none",
        captureAdminRequestContext(active.key),
      );
      setResult(registered);
      toast.success(bt("bloem.admin.pushRelay.registeredToast"));
    } catch (error) {
      setResult(null);
      toast.error(bt("bloem.admin.pushRelay.registerFailed"), {
        description: error instanceof Error ? error.message : undefined,
      });
    } finally {
      setPending(false);
    }
  };

  return (
    <FieldGroup
      label={bt("bloem.admin.pushRelay.title")}
      description={bt("bloem.admin.pushRelay.description")}
    >
      <div className="space-y-2 py-3">
        <p className="text-muted-foreground text-xs">{bt("bloem.admin.pushRelay.help")}</p>
        <Button variant="outline" size="sm" disabled={pending} onClick={() => void register()}>
          {pending ? (
            <Loader2 className="me-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <KeyRound className="me-1.5 h-3.5 w-3.5" />
          )}
          {bt("bloem.admin.pushRelay.register")}
        </Button>
        {result && (
          <p role="status" className="text-xs text-emerald-500">
            {bt("bloem.admin.pushRelay.registered", {
              deployment: result.deployment_id,
              expires: formatDateTime(result.expires_at),
            })}
          </p>
        )}
      </div>
    </FieldGroup>
  );
}
