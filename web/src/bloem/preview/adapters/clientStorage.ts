// Silo's api/client.ts reads browser storage through this module
// (bloem-preview-plugin.ts). On the guest host a preview guest's refresh token
// and profile come from Bloem's guest session: a guest never picks a profile,
// and its session carries the shared account's, which scopes the v2 reads that
// require X-Profile-Id. Everyone else gets Silo's storage unchanged.
import { guestHost, guestProfileId, guestRefreshToken } from "@/bloem/preview/session";
import { storage as siloStorage } from "@/utils/storage";

export * from "@/utils/storage";

export const storage: typeof siloStorage = {
  ...siloStorage,
  get(key) {
    if (guestHost() && key === siloStorage.KEYS.REFRESH_TOKEN) return guestRefreshToken();
    if (guestHost() && key === siloStorage.KEYS.PROFILE_ID) return guestProfileId();
    return siloStorage.get(key);
  },
};
