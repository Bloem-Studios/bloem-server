// Silo's useAuth.tsx reads the stored impersonation session through this
// module (bloem-preview-plugin.ts). A preview guest never impersonates, so on
// the guest host there is no stored session to read or clear.
import { guestHost } from "@/bloem/preview/session";
import {
  clearStoredImpersonationAdminSession as clearSiloSession,
  loadStoredImpersonationAdminSession as loadSiloSession,
} from "@/lib/impersonationSession";

export * from "@/lib/impersonationSession";

export function loadStoredImpersonationAdminSession(): ReturnType<typeof loadSiloSession> {
  return guestHost() ? null : loadSiloSession();
}

export function clearStoredImpersonationAdminSession(): void {
  if (!guestHost()) clearSiloSession();
}
