// @vitest-environment jsdom
import { StrictMode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { nativeApi, nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import { XtreamProviderForm } from "./XtreamProviderForm";

const authority = vi.hoisted(() => ({
  generation: 100,
  profileId: "parent" as string | null,
  pin: 1,
  role: "admin",
  supported: true,
}));
vi.mock("@/hooks/queries/useLiveTVAccess", () => ({
  useLiveTVAccess: () => ({
    data: { xtream_supported: authority.supported },
    isLoading: false,
    isError: false,
  }),
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { role: authority.role } }) }));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureSessionIdentity: () => ({
    authContextVersion: authority.generation,
    serverOrigin: "https://server.test",
  }),
  isSessionIdentityCurrent: (value: { authContextVersion: number }) =>
    value.authContextVersion === authority.generation,
  getProfileTokenGeneration: () => authority.pin,
  captureProfileRequestContext: () =>
    authority.profileId
      ? {
          accessToken: "fixture-account-token",
          serverOrigin: "https://server.test",
          authContextVersion: authority.generation,
          profileId: authority.profileId,
          profileToken: null,
          profileTokenGeneration: authority.pin,
        }
      : null,
}));
vi.mock("@/api/bloemClient", async (original) => ({
  ...(await original<typeof import("@/api/bloemClient")>()),
  getProfileId: () => authority.profileId,
  nativeApiWithProfileRequestContext: vi.fn(),
  nativeApi: vi.fn(),
}));
const request = vi.mocked(nativeApiWithProfileRequestContext);
const accountRequest = vi.mocked(nativeApi);
const saved = {
  id: "provider-1",
  type: "xtream",
  model: "Fixture TV",
  base_url: "https://provider.test",
  tuner_count: 1,
  channel_count: 1,
};
function setup(strict = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      {strict ? (
        <StrictMode>
          <XtreamProviderForm />
        </StrictMode>
      ) : (
        <XtreamProviderForm />
      )}
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, client, refresh: () => view.rerender(tree()) };
}
beforeEach(() => {
  authority.generation++;
  authority.profileId = "parent";
  authority.pin = 1;
  authority.role = "admin";
  authority.supported = true;
  request.mockReset();
  accountRequest.mockReset();
  request.mockImplementation(async (_path, _scope, options) =>
    options?.method === "POST" ? saved : { tuners: [saved] },
  );
  accountRequest.mockImplementation(async (_path, options) =>
    options?.method === "POST" ? saved : { tuners: [saved] },
  );
});
afterEach(cleanup);
async function fill() {
  fireEvent.change(await screen.findByLabelText("Provider base URL"), {
    target: { value: "https://provider.test" },
  });
  fireEvent.change(screen.getByLabelText("Provider name (optional)"), {
    target: { value: "Fixture TV" },
  });
  fireEvent.change(screen.getByLabelText("Provider username"), {
    target: { value: "fixture-user-private" },
  });
  fireEvent.change(screen.getByLabelText("Provider password"), {
    target: { value: "fixture-password-private" },
  });
}
function submit() {
  fireEvent.click(screen.getByRole("button", { name: "Add Xtream provider" }));
}
function writes() {
  return request.mock.calls.filter((call) => call[2]?.method === "POST");
}

it("creates once with captured authority, clears credentials, and confirms saved state without mutation-cache secrets", async () => {
  const { client } = setup();
  await fill();
  submit();
  await screen.findByText(/Provider added/);
  expect(writes()).toHaveLength(1);
  expect(writes()[0]?.[0]).toBe("/livetv/tuners/xtream");
  expect(writes()[0]?.[3]).toBe("none");
  expect(writes()[0]?.[1]).toMatchObject({
    profileId: "parent",
    authContextVersion: authority.generation,
  });
  expect(JSON.parse(String(writes()[0]?.[2]?.body))).toMatchObject({
    type: "xtream",
    name: "Fixture TV",
    username: "fixture-user-private",
    password: "fixture-password-private",
    max_connections: 1,
  });
  expect(screen.getByLabelText("Provider password")).toHaveValue("");
  expect(screen.getByLabelText("Provider username")).toHaveValue("");
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data),
    ),
  ).not.toMatch(/fixture-user-private|fixture-password-private/);
  expect(request.mock.calls.filter((call) => !call[2]?.method)).toHaveLength(2);
});

it("blocks after a lost response across remounts and requires explicit readback, never replaying the POST", async () => {
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method === "POST")
      throw new Error("private provider diagnostics fixture-password-private");
    return { tuners: [saved] };
  });
  const view = setup();
  await fill();
  submit();
  await screen.findByRole("alert");
  expect(screen.queryByText(/private provider diagnostics/)).toBeNull();
  expect(screen.queryByLabelText("Provider password")).toBeNull();
  expect(writes()).toHaveLength(1);
  view.unmount();
  setup();
  expect(screen.getByRole("button", { name: "Reload providers" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Add Xtream provider" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Reload providers" }));
  expect(await screen.findByLabelText("Provider password")).toHaveValue("");
  expect(writes()).toHaveLength(1);
});

it("treats a failed post-success readback as unresolved rather than allowing another creation", async () => {
  let reads = 0;
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method === "POST") return saved;
    if (++reads > 1) throw new Error("readback offline");
    return { tuners: [] };
  });
  setup();
  await fill();
  submit();
  await screen.findByRole("alert");
  expect(screen.queryByRole("button", { name: "Add Xtream provider" })).toBeNull();
  expect(writes()).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Reload providers" }));
  await screen.findByRole("alert");
  expect(writes()).toHaveLength(1);
});

it("rejects stale profile/PIN intent before fetch and clears its password", async () => {
  setup();
  await fill();
  authority.pin++;
  submit();
  expect(writes()).toHaveLength(0);
  expect(screen.getByLabelText("Provider password")).toHaveValue("");
});

it("aborts on authority change and ignores a late completion without polluting the new scope", async () => {
  let resolve!: (value: typeof saved) => void;
  request.mockImplementation(async (_path, _scope, options) =>
    options?.method === "POST"
      ? new Promise((done) => {
          resolve = done;
        })
      : { tuners: [] },
  );
  const view = setup();
  await fill();
  submit();
  const signal = writes()[0]?.[2]?.signal;
  await screen.findByText(/Adding provider and confirming/);
  authority.generation++;
  view.refresh();
  expect(signal?.aborted).toBe(true);
  await screen.findByLabelText("Provider password");
  await act(async () => resolve(saved));
  expect(screen.queryByText(/Provider added/)).toBeNull();
  expect(screen.getByLabelText("Provider password")).toHaveValue("");
  expect(writes()).toHaveLength(1);
  expect(
    JSON.stringify(
      view.client
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data),
    ),
  ).not.toContain("provider-1");
});

it("retains pending admission across remounts until the original operation settles", async () => {
  let reject!: (reason: Error) => void;
  request.mockImplementation(async (_path, _scope, options) =>
    options?.method === "POST"
      ? new Promise((_resolve, fail) => {
          reject = fail;
        })
      : { tuners: [] },
  );
  const view = setup();
  await fill();
  submit();
  view.unmount();
  setup();
  expect(screen.getByText(/Adding provider and confirming/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Reload providers" })).toBeNull();
  await act(async () => reject(new Error("request ended")));
  await screen.findByRole("button", { name: "Reload providers" });
  expect(writes()).toHaveLength(1);
});

it("retains the native Live TV profile requirement instead of falling back to a profileless request", () => {
  authority.profileId = null;
  setup();
  expect(screen.getByText(/Select your primary profile/)).toBeInTheDocument();
  expect(screen.queryByLabelText("Provider password")).toBeNull();
  expect(request).not.toHaveBeenCalled();
  expect(accountRequest).not.toHaveBeenCalled();
});

it("does not accept credentials when the server lacks the advertised Xtream capability", () => {
  authority.supported = false;
  setup();
  expect(screen.getByText(/does not advertise Xtream/)).toBeInTheDocument();
  expect(screen.queryByLabelText("Provider password")).toBeNull();
  expect(request).not.toHaveBeenCalled();
});

it("survives Strict Mode read cancellation without stranding initial admission", async () => {
  setup(true);
  await screen.findByLabelText("Provider password");
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(writes()).toHaveLength(0);
});

it("does not mount credential inputs or make requests for a non-administrator", () => {
  authority.role = "user";
  setup();
  expect(screen.queryByText("Xtream live TV provider")).toBeNull();
  expect(request).not.toHaveBeenCalled();
  expect(accountRequest).not.toHaveBeenCalled();
});
