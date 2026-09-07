import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { hasDurableLifecycle, replanDurableSession } from "./lifecycle-v2";
import { registerDurableSessionMutations } from "./session-mutations";
import { PlayerFetchError } from "./player-fetch";
import type { PlayerConfig } from "./context/PlayerConfigContext";
import { buildReplanRequestV3 } from "./playback-session-wire-v3";
import {
  fixtureClientCapabilitiesV3,
  fixtureClientPlaybackContextV3,
  fixturePlanV3,
} from "./protocol-v3.fixtures";

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
const plan = fixturePlanV3();
const replan = buildReplanRequestV3({
  operation: "seek_reanchor",
  positionSeconds: 42.5,
  plan,
  playbackAttemptId: "attempt-0123456789",
  replanRequestId: "replan-0123456789",
  planAttemptId: "plan-attempt-0123",
  qualityPreference: "auto",
  attemptedPlanKeys: [],
  attemptCount: 1,
  metered: false,
  clientCapabilities: fixtureClientCapabilitiesV3(),
  clientPlaybackContext: fixtureClientPlaybackContextV3(),
});
const wireDecision = {
  protocol_version: 3,
  server_features: ["sequenced_progress_v1"],
  outcome: "playable",
  session_id: sessionId,
  playback_plan: {
    ...plan,
    session_id: sessionId,
    requested_media_file_id: "42",
    effective_media_file_id: "42",
    source: { ...plan.source, media_file_id: "42" },
  },
};
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

it("recognises only durable sessions", () => {
  expect(hasDurableLifecycle(sessionId)).toBe(true);
  expect(hasDurableLifecycle("other")).toBe(false);
  expect(hasDurableLifecycle(null)).toBe(false);
});

it("replans through v2 with the captured installation and origin", async () => {
  const fetcher = vi.fn().mockResolvedValue(reply(wireDecision));
  vi.stubGlobal("fetch", fetcher);
  const decision = await replanDurableSession(config, sessionId, replan);
  const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
  expect(url).toBe(`http://localhost:3000/api/v2/playback/${sessionId}/replan`);
  expect(init.method).toBe("POST");
  const headers = new Headers(init.headers);
  expect(headers.get("Authorization")).toBe("Bearer token");
  expect(headers.get("X-Profile-Id")).toBe("profile");
  const sent = JSON.parse(String(init.body)) as Record<string, unknown>;
  expect(sent.installation_id).toBe(installationId);
  expect(sent.replan_request_id).toBe("replan-0123456789");
  expect(sent.operation).toBe("seek_reanchor");
  expect(decision.playback_plan?.requested_media_file_id).toBe(42);
  expect(decision.playback_plan?.source.media_file_id).toBe(42);
});

it("surfaces a problem refusal as a player fetch error with its code", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      reply(
        {
          type: "https://siloserver.org/docs/api/v2/problems/capability_unsupported",
          title: "Capability unsupported",
          status: 501,
          detail:
            "The initial playback flow cannot change tracks, quality or output; start a new attempt",
        },
        501,
        "application/problem+json",
      ),
    ),
  );
  const failure = await replanDurableSession(config, sessionId, replan).catch((e: unknown) => e);
  expect(failure).toBeInstanceOf(PlayerFetchError);
  expect((failure as PlayerFetchError).status).toBe(501);
  expect((failure as PlayerFetchError).code).toBe("capability_unsupported");
  expect((failure as PlayerFetchError).message).toContain("start a new attempt");
});

it("refuses a replan whose authority is not current, before and after dispatch", async () => {
  const fetcher = vi.fn().mockImplementation(async () => {
    current = false;
    return reply(wireDecision);
  });
  vi.stubGlobal("fetch", fetcher);
  await expect(replanDurableSession(config, sessionId, replan)).rejects.toThrow("identity changed");
  await expect(replanDurableSession(config, sessionId, replan)).rejects.toThrow("identity changed");
  expect(fetcher).toHaveBeenCalledTimes(1);
  await expect(replanDurableSession(config, "other", replan)).rejects.toThrow(
    "no durable authority",
  );
});
