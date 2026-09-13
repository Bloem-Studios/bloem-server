import { afterEach, beforeEach, expect, it, vi } from "vitest";

// jsdom's Storage objects are proxy-backed, so a vi.spyOn against
// Storage.prototype (or against the instance) never intercepts -- the spy
// installs cleanly and then simply does not fire. Both tests below relied on
// that to simulate a storage failure, so one of them silently stopped
// exercising its failure path while still passing. Replace the whole object
// instead, which does take effect.
function stubStorage(name: "localStorage" | "sessionStorage", overrides: Partial<Storage>): void {
  const real = globalThis[name];
  const backing = new Map<string, string>();
  for (let i = 0; i < real.length; i += 1) {
    const key = real.key(i)!;
    backing.set(key, real.getItem(key)!);
  }
  const stub: Storage = {
    get length() {
      return backing.size;
    },
    key: (index: number) => [...backing.keys()][index] ?? null,
    getItem: (key: string) => backing.get(key) ?? null,
    setItem: (key: string, value: string) => void backing.set(key, String(value)),
    removeItem: (key: string) => void backing.delete(key),
    clear: () => backing.clear(),
    ...overrides,
  };
  vi.stubGlobal(name, stub);
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  vi.resetModules();
});
afterEach(() => vi.restoreAllMocks());

it("advances synchronously on proof installation, replacement, removal and reinstall", async () => {
  const client = await import("./client");
  client.setAccessToken("account");
  client.setProfileId("profile");
  const initial = client.captureProfileRequestContext()!;
  let prior = initial.profileTokenGeneration!;
  for (const proof of [
    "first-private-proof",
    "replacement-private-proof",
    null,
    "first-private-proof",
  ]) {
    client.setProfileToken(proof);
    expect(client.getProfileTokenGeneration()).toBe(prior + 1);
    const captured = client.captureProfileRequestContext()!;
    expect(captured.profileTokenGeneration).toBe(prior + 1);
    expect(captured.profileToken).toBe(proof);
    expect(captured.authContextVersion).toBe(initial.authContextVersion);
    expect(captured.profileId).toBe(initial.profileId);
    expect(client.isProfileRequestContextCurrent(initial)).toBe(true);
    const key = [
      captured.serverOrigin,
      captured.authContextVersion,
      captured.profileId,
      captured.profileTokenGeneration,
    ];
    expect(JSON.stringify(key)).not.toContain("private-proof");
    prior = captured.profileTokenGeneration!;
  }
  expect(initial.profileTokenGeneration).toBeLessThan(prior);
  const current = client.captureProfileRequestContext()!;
  client.setProfileToken(current.profileToken);
  expect(client.getProfileTokenGeneration()).toBe(prior + 1);
  expect(client.isCapturedProfileAuthorityActive(current)).toBe(true);
});

it("advances before current-proof rejection notifies listeners, but ignores old rejections", async () => {
  const client = await import("./client");
  client.setAccessToken("account");
  client.setProfileId("profile");
  client.setProfileToken("old-proof");
  const old = client.captureProfileRequestContext()!;
  client.setProfileToken("current-proof");
  const current = client.captureProfileRequestContext()!;
  const notified = vi.fn(() => {
    expect(client.getProfileToken()).toBeNull();
    expect(client.getProfileTokenGeneration()).toBe(current.profileTokenGeneration! + 1);
  });
  client.onProfileUnverified(notified);
  client.reportProfileUnverified(old.profileId, old.profileToken, old);
  expect(client.getProfileTokenGeneration()).toBe(current.profileTokenGeneration);
  expect(notified).not.toHaveBeenCalled();
  client.reportProfileUnverified(current.profileId, current.profileToken, current);
  expect(notified).toHaveBeenCalledOnce();
  expect(localStorage.getItem("profile_token")).toBeNull();
  expect(sessionStorage.getItem("profile_token")).toBeNull();
  client.onProfileUnverified(null);
});

it.each(["local", "legacy session"])(
  "restores %s proof once and keeps capture/read paths pure",
  async (source) => {
    (source === "local" ? localStorage : sessionStorage).setItem(
      "profile_token",
      "persisted-proof",
    );
    const client = await import("./client");
    expect(client.getProfileToken()).toBe("persisted-proof");
    expect(localStorage.getItem("profile_token")).toBe("persisted-proof");
    if (source === "legacy session") expect(sessionStorage.getItem("profile_token")).toBeNull();
    client.setAccessToken("account");
    client.setProfileId("profile");
    const generation = client.getProfileTokenGeneration();
    const writes = vi.spyOn(Storage.prototype, "setItem");
    const removals = vi.spyOn(Storage.prototype, "removeItem");
    for (let i = 0; i < 3; i++) {
      expect(client.captureProfileRequestContext()?.profileTokenGeneration).toBe(generation);
      expect(client.getProfileToken()).toBe("persisted-proof");
      expect(client.getProfileTokenGeneration()).toBe(generation);
    }
    expect(writes).not.toHaveBeenCalled();
    expect(removals).not.toHaveBeenCalled();
  },
);

it("remains authoritative in memory when persistence fails during removal", async () => {
  const client = await import("./client");
  client.setProfileToken("proof");
  const generation = client.getProfileTokenGeneration();
  stubStorage("localStorage", {
    removeItem: () => {
      throw new Error("storage unavailable");
    },
  });
  client.setProfileToken(null);
  expect(client.getProfileTokenGeneration()).toBe(generation + 1);
  expect(client.getProfileToken()).toBeNull();
  expect(client.getProfileToken()).toBeNull();
});

it("retains restored legacy proof when startup cleanup throws", async () => {
  sessionStorage.setItem("profile_token", "legacy-proof");
  stubStorage("sessionStorage", {
    removeItem: () => {
      throw new Error("cleanup unavailable");
    },
  });
  const removals = vi.spyOn(globalThis.sessionStorage, "removeItem");
  const client = await import("./client");
  expect(client.getProfileToken()).toBe("legacy-proof");
  expect(localStorage.getItem("profile_token")).toBe("legacy-proof");
  expect(sessionStorage.getItem("profile_token")).toBe("legacy-proof");
  client.setAccessToken("account");
  client.setProfileId("profile");
  const generation = client.getProfileTokenGeneration();
  removals.mockClear();
  const writes = vi.spyOn(Storage.prototype, "setItem");
  for (let i = 0; i < 3; i++) {
    const captured = client.captureProfileRequestContext()!;
    expect(captured.profileToken).toBe("legacy-proof");
    expect(captured.profileTokenGeneration).toBe(generation);
    expect(client.getProfileToken()).toBe("legacy-proof");
  }
  expect(writes).not.toHaveBeenCalled();
  expect(removals).not.toHaveBeenCalled();
});
