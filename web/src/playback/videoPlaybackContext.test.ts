import { afterEach, expect, it, vi } from "vitest";
import { captureVideoPlaybackContext } from "./videoPlaybackContext";
import { captureProfileRequestContext, isCapturedProfileAuthorityActive } from "@/api/client";
vi.mock("@/api/client", () => ({
  captureProfileRequestContext: vi.fn(),
  isCapturedProfileAuthorityActive: vi.fn(),
}));
afterEach(() => vi.resetAllMocks());
it("keeps the original bearer and profile while guarding the original authority", () => {
  const captured = {
    accessToken: "original",
    authContextVersion: 1,
    serverOrigin: "https://api.example.test",
    profileId: "profile",
    profileToken: null,
  };
  vi.mocked(captureProfileRequestContext).mockReturnValue(captured);
  vi.mocked(isCapturedProfileAuthorityActive).mockReturnValue(true);
  const context = captureVideoPlaybackContext("account")!;
  vi.mocked(captureProfileRequestContext).mockReturnValue({
    ...captured,
    accessToken: "new-ambient",
    profileId: "other",
  });
  expect(context.mediaRequestHeaders?.()).toEqual({
    Authorization: "Bearer original",
    "X-Profile-Id": "profile",
  });
  expect(captureProfileRequestContext).toHaveBeenCalledTimes(1);
  expect(isCapturedProfileAuthorityActive).toHaveBeenLastCalledWith(captured);
  expect(JSON.stringify(context)).not.toContain("original");
  vi.mocked(isCapturedProfileAuthorityActive).mockReturnValue(false);
  expect(context.mediaRequestHeaders?.()).toBeNull();
  expect(context.isCurrent()).toBe(false);
});
it("cannot supply auxiliary credentials without a captured login", () => {
  vi.mocked(captureProfileRequestContext).mockReturnValue(null);
  expect(captureVideoPlaybackContext("account")).toBeNull();
});
