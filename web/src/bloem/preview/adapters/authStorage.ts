// Silo's useAuth.tsx reads browser storage through this module
// (bloem-preview-plugin.ts). A preview guest's session lives in Bloem's guest
// store instead: its refresh token is the guest's, its profile is the shared
// account's and stays put, and it starts no profile epoch. Everyone else gets
// Silo's storage unchanged.
import { guestHost, guestRefreshToken } from "@/bloem/preview/session";
import { endProfileEpoch as endSiloProfileEpoch, storage as siloStorage } from "@/utils/storage";

export * from "@/utils/storage";

export const storage: typeof siloStorage = {
  ...siloStorage,
  get(key) {
    if (guestHost() && key === siloStorage.KEYS.REFRESH_TOKEN) return guestRefreshToken();
    return siloStorage.get(key);
  },
  remove(key) {
    if (guestHost() && key === siloStorage.KEYS.CURRENT_PROFILE) return;
    siloStorage.remove(key);
  },
};

export function endProfileEpoch(): void {
  if (guestHost()) return;
  endSiloProfileEpoch();
}
