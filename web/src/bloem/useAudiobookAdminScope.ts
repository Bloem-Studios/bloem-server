import { captureAdminRequestContext } from "@/api/adminV2Client";
import { captureSessionIdentity } from "@/api/client";
import { useAdminContext } from "@/contexts/AdminContextProvider";
export function useAudiobookAdminScope() {
  const { active, switching } = useAdminContext();
  const context = active ? captureAdminRequestContext(active.key) : null;
  const identity = captureSessionIdentity();
  const key = JSON.stringify([
    context?.key,
    context?.generation,
    identity.serverOrigin,
    identity.authContextVersion,
  ]);
  return {
    key,
    context,
    switching,
    allowed: active?.scope === "organization" && !!context && !switching,
    editing:
      active?.scope === "organization" && active.authority === "platform_admin" && !switching,
    current: () => {
      const live = context ? captureAdminRequestContext(context.key) : null;
      const now = captureSessionIdentity();
      return (
        !!live &&
        live.generation === context?.generation &&
        identity.serverOrigin === now.serverOrigin &&
        identity.authContextVersion === now.authContextVersion
      );
    },
  };
}
