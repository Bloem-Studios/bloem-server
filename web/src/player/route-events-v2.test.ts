import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { reportDurableRouteEvent } from "./route-events-v2";
import { registerDurableSessionMutations } from "./session-mutations";
import type { PlayerConfig } from "./context/PlayerConfigContext";

let current = true;
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
  capturePlaybackMutationContext: () => ({
    accountId: "account",
    profileId: "profile",
    origin: "http://localhost:3000",
    isCurrent: () => current,
  }),
};
const sessionId = "11111111-1111-4111-8111-111111111111";
const installationId = "22222222-2222-4222-8222-222222222222";
const reply = (body: unknown, status = 200, type = "application/json") =>
  new Response(body === null ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": type },
  });

beforeEach(async () => {
  current = true;
  localStorage.clear();
  vi.stubGlobal("navigator", {
    locks: { request: (_: string, __: unknown, run: () => Promise<unknown>) => run() },
  });
  await registerDurableSessionMutations(config, sessionId, installationId);
});
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

it("reports route events once with a fresh id and never throws", async () => {
  const fetcher = vi.fn().mockResolvedValue(reply({ event_id: "x", outcome: "accepted" }, 202));
  vi.stubGlobal("fetch", fetcher);
  const event = {
    protocol_version: 3,
    playback_attempt_id: "attempt-0123456789",
    session_id: sessionId,
    event: "first_frame" as const,
    diagnostics: { decoder_name: "synthetic" },
  };
  await reportDurableRouteEvent(config, sessionId, event);
  await reportDurableRouteEvent(config, sessionId, event);
  expect(fetcher).toHaveBeenCalledTimes(2);
  const bodies = fetcher.mock.calls.map(
    ([, init]) => JSON.parse(String((init as RequestInit).body)) as Record<string, unknown>,
  );
  expect(bodies[0]!.installation_id).toBe(installationId);
  expect(typeof bodies[0]!.event_id).toBe("string");
  expect(bodies[0]!.event_id).not.toBe(bodies[1]!.event_id);
  expect(fetcher.mock.calls[0]![0]).toBe("http://localhost:3000/api/v2/playback/route-events");
  fetcher.mockRejectedValue(new TypeError("offline"));
  await expect(reportDurableRouteEvent(config, sessionId, event)).resolves.toBeUndefined();
  current = false;
  await reportDurableRouteEvent(config, sessionId, event);
  expect(fetcher).toHaveBeenCalledTimes(3);
});
