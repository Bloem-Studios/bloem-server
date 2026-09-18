// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setAccessToken, setProfileId, setProfileToken, setRefreshToken } from "@/api/client";
import SeasonalLayer from "./SeasonalLayer";
import type { SeasonalPack } from "./schedule";

const viewer = vi.hoisted(() => ({ profileId: "viewer", signedIn: true, playing: false }));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    user: viewer.signedIn ? { id: 1 } : null,
    profile: viewer.profileId ? { id: viewer.profileId } : null,
  }),
}));
vi.mock("@/playback/watchPlaybackContext", () => ({
  useWatchPlaybackController: () => ({ state: { request: viewer.playing ? {} : null } }),
}));

const pack = (id: string): SeasonalPack => ({
  id,
  effect_id: "artwork",
  intensity: 0.5,
  surfaces: ["all"],
  window: { starts_at: "2020-01-01T00:00:00Z", ends_at: "2099-01-01T00:00:00Z" },
  assets: { banner_url: `https://cdn.example/${id}.webp` },
});
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
let client: QueryClient;
let requests: { path: string; options?: RequestInit }[];
let respond: (path: string) => Response | Promise<Response>;
let reducedMotion: boolean;
let motionListener: (() => void) | undefined;

function mount(path = "/") {
  const tree = () => (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <SeasonalLayer />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, update: () => view.rerender(tree()) };
}
function paths() {
  return requests.map((request) => request.path);
}
function artwork(id: string) {
  return document.querySelector(`img[src="https://cdn.example/${id}.webp"]`);
}

beforeEach(() => {
  localStorage.clear();
  viewer.profileId = "viewer";
  viewer.signedIn = true;
  viewer.playing = false;
  reducedMotion = false;
  motionListener = undefined;
  setAccessToken("test-account-token");
  setProfileId("viewer");
  setProfileToken("test-pin-proof");
  setRefreshToken(null);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  requests = [];
  respond = (path) => {
    if (path === "/api/bloem/v1/capabilities")
      return json({ features: {}, feature_tokens: ["seasonal_viewer_v1"] });
    if (path === "/api/bloem/v1/ambience") return json({ ambience: [pack("organization")] });
    if (path === "/api/v1/theme/branding") return json({ ambience: [pack("public")] });
    throw new Error(`Unexpected request: ${path}`);
  };
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, options?: RequestInit) => {
      requests.push({ path: url, options });
      return Promise.resolve(respond(url));
    }),
  );
  vi.stubGlobal("matchMedia", () => ({
    get matches() {
      return reducedMotion;
    },
    addEventListener: (_: string, listener: () => void) => {
      motionListener = listener;
    },
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  cleanup();
  client.clear();
  setAccessToken(null);
  setProfileId(null);
  setProfileToken(null);
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it("delivers organization artwork through the capability-gated native viewer request with captured profile and PIN authority", async () => {
  mount();
  await waitFor(() => expect(artwork("organization")).not.toBeNull());
  expect(paths()).toEqual(["/api/bloem/v1/capabilities", "/api/bloem/v1/ambience"]);
  const headers = new Headers(requests[1]!.options?.headers);
  expect(headers.get("Authorization")).toBe("Bearer test-account-token");
  expect(headers.get("X-Profile-Id")).toBe("viewer");
  expect(headers.get("X-Profile-Token")).toBe("test-pin-proof");
  expect(artwork("public")).toBeNull();
});

it.each([false, true])("keeps login presentation public-only (signed in: %s)", async (signedIn) => {
  viewer.signedIn = signedIn;
  if (!signedIn) {
    viewer.profileId = "";
    setAccessToken(null);
    setProfileId(null);
  }
  mount("/login");
  await waitFor(() => expect(artwork("public")).not.toBeNull());
  expect(paths()).toEqual(["/api/v1/theme/branding"]);
  expect(new Headers(requests[0]!.options?.headers).has("Authorization")).toBe(false);
  expect(requests[0]!.options?.credentials).toBe("omit");
  expect(artwork("organization")).toBeNull();
});

it.each(["missing token", "unsupported probe"])(
  "uses credential-free public fallback for %s",
  async (mode) => {
    respond = (path) =>
      path === "/api/bloem/v1/capabilities"
        ? json({ features: {}, feature_tokens: [] }, mode === "unsupported probe" ? 404 : 200)
        : json({ ambience: [pack("public")] });
    mount();
    await waitFor(() => expect(artwork("public")).not.toBeNull());
    expect(paths()).toEqual(["/api/bloem/v1/capabilities", "/api/v1/theme/branding"]);
    expect(new Headers(requests[1]!.options?.headers).has("Authorization")).toBe(false);
    expect(requests[1]!.options?.credentials).toBe("omit");
  },
);

it("does not use public branding as an authenticated fallback when viewer delivery is forbidden", async () => {
  const original = respond;
  respond = (path) =>
    path === "/api/bloem/v1/ambience"
      ? json({ error: "profile_unverified", message: "Profile verification required" }, 403)
      : original(path);
  const view = mount();
  await waitFor(() =>
    expect(
      client
        .getQueryCache()
        .findAll({ queryKey: ["seasonal-branding"] })
        .some((query) => query.state.status === "error"),
    ).toBe(true),
  );
  expect(paths()).toEqual(["/api/bloem/v1/capabilities", "/api/bloem/v1/ambience"]);
  expect(document.querySelector("img")).toBeNull();
  respond = original;
  setProfileToken("newly-verified-pin-proof");
  view.update();
  await waitFor(() => expect(artwork("organization")).not.toBeNull());
  expect(paths()).toEqual([
    "/api/bloem/v1/capabilities",
    "/api/bloem/v1/ambience",
    "/api/bloem/v1/ambience",
  ]);
});

it("waits for matching captured profile authority before authenticated home delivery", () => {
  setProfileId("other-profile");
  mount();
  expect(paths()).toEqual([]);
  expect(document.querySelector("img")).toBeNull();
});

it.each(["account", "profile", "PIN"])(
  "fences a late native response across a %s change",
  async (change) => {
    let finish!: (response: Response) => void;
    const original = respond;
    respond = (path) =>
      path === "/api/bloem/v1/ambience"
        ? new Promise<Response>((resolve) => {
            finish = resolve;
          })
        : original(path);
    const view = mount();
    await waitFor(() => expect(paths()).toContain("/api/bloem/v1/ambience"));
    if (change === "account") setAccessToken("new-account-token");
    if (change === "profile") {
      viewer.profileId = "new-profile";
      setProfileId("new-profile");
    }
    if (change === "PIN") setProfileToken("new-pin-proof");
    respond = (path) =>
      path === "/api/bloem/v1/ambience" ? json({ ambience: [pack("current")] }) : original(path);
    view.update();
    await waitFor(() => expect(artwork("current")).not.toBeNull());
    await act(async () => finish(json({ ambience: [pack("previous")] })));
    expect(artwork("previous")).toBeNull();
    expect(artwork("current")).not.toBeNull();
    const cache = JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => ({ key: query.queryKey, data: query.state.data })),
    );
    expect(cache).not.toContain("test-account-token");
    expect(cache).not.toContain("test-pin-proof");
  },
);

it("rejects a late response after a PIN proof is removed and reinstalled, even before rerender", async () => {
  let finish!: (response: Response) => void;
  const original = respond;
  respond = (path) =>
    path === "/api/bloem/v1/ambience"
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : original(path);
  mount();
  await waitFor(() => expect(paths()).toContain("/api/bloem/v1/ambience"));
  setProfileToken(null);
  setProfileToken("test-pin-proof");
  await act(async () => finish(json({ ambience: [pack("previous")] })));
  await waitFor(() =>
    expect(
      client
        .getQueryCache()
        .findAll({ queryKey: ["seasonal-branding"] })
        .some((query) => query.state.status === "error"),
    ).toBe(true),
  );
  expect(artwork("previous")).toBeNull();
});

it("clears private artwork on logout and rejects a previous login's late response", async () => {
  let finish!: (response: Response) => void;
  const original = respond;
  respond = (path) =>
    path === "/api/bloem/v1/ambience"
      ? new Promise<Response>((resolve) => {
          finish = resolve;
        })
      : original(path);
  const view = mount();
  await waitFor(() => expect(paths()).toContain("/api/bloem/v1/ambience"));
  viewer.signedIn = false;
  viewer.profileId = "";
  setAccessToken(null);
  setProfileId(null);
  view.update();
  await waitFor(() => expect(artwork("public")).not.toBeNull());
  await act(async () => finish(json({ ambience: [pack("previous")] })));
  expect(artwork("previous")).toBeNull();
  expect(artwork("public")).not.toBeNull();
});

it("preserves opt-out, playback suppression and live reduced-motion changes", async () => {
  localStorage.setItem("bloem-seasonal-effects", "off");
  const view = mount();
  await screen.findByRole("button", { name: "Seasonal effects: Off" });
  expect(artwork("organization")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Seasonal effects: Off" }));
  expect(artwork("organization")).not.toBeNull();
  expect(localStorage.getItem("bloem-seasonal-effects")).toBe("on");
  viewer.playing = true;
  view.update();
  expect(artwork("organization")).toBeNull();
  viewer.playing = false;
  view.update();
  expect(artwork("organization")).not.toBeNull();
  act(() => {
    reducedMotion = true;
    motionListener?.();
  });
  expect(artwork("organization")).toBeNull();
  act(() => {
    reducedMotion = false;
    motionListener?.();
  });
  expect(artwork("organization")).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Seasonal effects: On" }));
  expect(artwork("organization")).toBeNull();
  expect(localStorage.getItem("bloem-seasonal-effects")).toBe("off");
});

it("removes delivery at its exclusive end time without waiting for a refetch", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2030-01-01T00:00:00Z"));
  const original = respond;
  respond = (path) =>
    path === "/api/bloem/v1/ambience"
      ? json({
          ambience: [
            {
              ...pack("ending"),
              window: { starts_at: "2029-12-01T00:00:00Z", ends_at: "2030-01-01T00:00:02Z" },
            },
          ],
        })
      : original(path);
  mount();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10);
  });
  expect(artwork("ending")).not.toBeNull();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
  expect(artwork("ending")).toBeNull();
});

it("drops artwork older than the freshness budget while a refetch is pending", async () => {
  vi.useFakeTimers();
  mount();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10);
  });
  expect(artwork("organization")).not.toBeNull();
  respond = () => new Promise<Response>(() => {});
  await act(async () => {
    await vi.advanceTimersByTimeAsync(46000);
  });
  expect(artwork("organization")).toBeNull();
});
