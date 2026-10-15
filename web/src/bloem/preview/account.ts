import { userFromAccount as originalUserFromAccount } from "../../api/v2/account";
import type { Account, TokenPair } from "../../api/v2/account";
import type { LoginResponse, User } from "@/api/types";
import { rememberGuestPresentation } from "./session";
export * from "../../api/v2/account";
export function userFromAccount(account: Account): User {
  const user = originalUserFromAccount(account);
  if (!account.preview) return user;
  rememberGuestPresentation(account.preview);
  return { ...user, preview: account.preview, playback_allowed: account.playback_allowed } as User;
}
export function sessionFromTokenPair(pair: TokenPair): LoginResponse {
  return {
    access_token: pair.access_token,
    refresh_token: pair.refresh_token,
    expires_in: pair.expires_in,
    user: userFromAccount(pair.user),
  };
}
