// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nativeApiWithProfileRequestContext as apiWithProfileRequestContext } from "@/api/bloemClient";
import type { LiveTVChannel, LiveTVSeriesRule } from "@/api/bloemTypes";
import { useCreateLiveTVSeriesRule } from "@/hooks/queries/useLiveTVSeriesRules";
import { LiveTVSeriesRules } from "./LiveTVSeriesRules";

const auth = vi.hoisted(() => ({ profileId: "profile-a", version: 1, pinGeneration: 1 }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ profile: { id: auth.profileId } }) }));
vi.mock("@/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/client")>()),
  captureProfileRequestContext: () => ({
    accessToken: "account-secret",
    profileId: auth.profileId,
    profileToken: `pin-secret-${auth.pinGeneration}`,
    authContextVersion: auth.version,
    serverOrigin: "https://server.test",
    profileTokenGeneration: auth.pinGeneration,
  }),
  isCapturedProfileAuthorityActive: (snapshot: {
    profileId: string;
    authContextVersion: number;
    profileTokenGeneration: number;
  }) =>
    snapshot.profileId === auth.profileId &&
    snapshot.authContextVersion === auth.version &&
    snapshot.profileTokenGeneration === auth.pinGeneration,
}));
vi.mock("@/api/bloemClient", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/bloemClient")>()),
  nativeApiWithProfileRequestContext: vi.fn(),
}));

const channel: LiveTVChannel = {
  id: "news",
  enabled: true,
  name: "World News",
  callsign: "WN",
  number: "4",
  tuner_id: "tuner-1",
  logo_url: "",
  hd: true,
  stream_url: "",
  guide_station_id: "",
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
const initialRule: LiveTVSeriesRule = {
  id: "rule/one",
  series_id: "",
  title_match: "Evening News",
  channel_id: "news",
  new_only: true,
  enabled: true,
  keep_last: 0,
};
let rules: LiveTVSeriesRule[];
const mockRequest = vi.mocked(apiWithProfileRequestContext);

function setup(channels: LiveTVChannel[] = [channel]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <LiveTVSeriesRules channels={channels} />
    </QueryClientProvider>
  );
  const view = render(tree());
  return { client, ...view, rerenderPage: () => view.rerender(tree()) };
}
function writes() {
  return mockRequest.mock.calls.filter(([, , options]) => options?.method);
}

beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    },
  );
  auth.profileId = "profile-a";
  auth.version = 1;
  auth.pinGeneration = 1;
  rules = [];
  mockRequest.mockReset();
  mockRequest.mockImplementation(async (path, snapshot, options) => {
    if (options?.method === "POST") {
      const created = { ...initialRule, ...JSON.parse(String(options.body)), id: "new-rule" };
      rules = [...rules, created];
      return created;
    }
    if (options?.method === "DELETE") {
      rules = rules.filter((rule) => !path.endsWith(encodeURIComponent(rule.id)));
      return undefined;
    }
    return { series_rules: snapshot.profileId === "profile-a" ? rules : [] };
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Live TV series rules", () => {
  it("loads an empty list and does not allow blank or whitespace-only titles", async () => {
    setup();
    expect(await screen.findByText("No series recording rules yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create rule" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Title contains"), { target: { value: "   " } });
    expect(screen.getByRole("button", { name: "Create rule" })).toBeDisabled();
    expect(writes()).toHaveLength(0);
  });

  it("creates a channel-filtered rule using captured profile authority without transport replay", async () => {
    const { client } = setup();
    await screen.findByText("No series recording rules yet.");
    fireEvent.change(screen.getByLabelText("Title contains"), { target: { value: "  News  " } });
    fireEvent.change(screen.getByLabelText("Channel"), { target: { value: "news" } });
    fireEvent.click(screen.getByRole("switch"));
    fireEvent.click(screen.getByRole("button", { name: "Create rule" }));
    expect(await screen.findByText("News")).toBeInTheDocument();
    expect(writes()).toEqual([
      [
        "/livetv/series-rules",
        expect.objectContaining({ profileId: "profile-a", authContextVersion: 1 }),
        {
          method: "POST",
          body: JSON.stringify({ title_match: "News", channel_id: "news", new_only: true }),
        },
        "none",
      ],
    ]);
    expect(screen.getByLabelText("Title contains")).toHaveValue("");
    const keys = JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    );
    expect(keys).not.toContain("account-secret");
    expect(keys).not.toContain("pin-secret");
  });

  it("requires confirmation to delete, encodes the rule ID, and reloads the list", async () => {
    rules = [initialRule];
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Remove rule: Evening News" }));
    expect(
      screen.getByText(/Already scheduled recordings and recorded files are not deleted/),
    ).toBeInTheDocument();
    expect(writes()).toHaveLength(0);
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove rule" }),
    );
    await screen.findByText("No series recording rules yet.");
    expect(writes()[0]).toEqual([
      "/livetv/series-rules/rule%2Fone",
      expect.objectContaining({ profileId: "profile-a" }),
      { method: "DELETE" },
      "none",
    ]);
  });

  it("supports cancelling removal and managing rules after all channels disappear", async () => {
    rules = [initialRule];
    setup([]);
    fireEvent.click(await screen.findByRole("button", { name: "Remove rule: Evening News" }));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }),
    );
    expect(writes()).toHaveLength(0);
    expect(screen.getByText(/Unavailable channel/)).toBeInTheDocument();
  });

  it("keeps a failed creation draft, reports the error and never automatically retries the write", async () => {
    setup();
    await screen.findByText("No series recording rules yet.");
    mockRequest.mockRejectedValueOnce(new Error("DVR is unavailable"));
    fireEvent.change(screen.getByLabelText("Title contains"), { target: { value: "News" } });
    fireEvent.click(screen.getByRole("button", { name: "Create rule" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("DVR is unavailable");
    expect(screen.getByLabelText("Title contains")).toHaveValue("News");
    expect(writes()).toHaveLength(1);
  });

  it("retains a rule on deletion failure", async () => {
    rules = [initialRule];
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Remove rule: Evening News" }));
    mockRequest.mockRejectedValueOnce(new Error("Not permitted"));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove rule" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Not permitted");
    expect(screen.getByText("Evening News")).toBeInTheDocument();
  });

  it("distinguishes a failed read from an empty list and allows reload", async () => {
    mockRequest.mockRejectedValueOnce(new Error("Rules unavailable"));
    setup();
    expect(await screen.findByRole("alert")).toHaveTextContent("Rules unavailable");
    expect(screen.queryByText("No series recording rules yet.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create rule" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Reload recording rules" }));
    expect(await screen.findByText("No series recording rules yet.")).toBeInTheDocument();
  });

  it("blocks duplicate submissions while a creation is pending", async () => {
    const pending = deferred<LiveTVSeriesRule>();
    setup();
    await screen.findByText("No series recording rules yet.");
    mockRequest.mockReturnValueOnce(pending.promise);
    fireEvent.change(screen.getByLabelText("Title contains"), { target: { value: "News" } });
    fireEvent.click(screen.getByRole("button", { name: "Create rule" }));
    expect(await screen.findByRole("button", { name: "Creating…" })).toBeDisabled();
    fireEvent.submit(screen.getByRole("form", { name: "Create recording rule" }));
    expect(writes()).toHaveLength(1);
    await act(async () => pending.resolve(initialRule));
    await waitFor(() => expect(screen.getByLabelText("Title contains")).toHaveValue(""));
  });

  it.each(["profile", "session", "pin"])(
    "drops drafts and confirmations after a %s change",
    async (kind) => {
      rules = [initialRule];
      const view = setup();
      fireEvent.click(await screen.findByRole("button", { name: "Remove rule: Evening News" }));
      fireEvent.change(screen.getByLabelText("Title contains"), { target: { value: "Draft" } });
      if (kind === "profile") auth.profileId = "profile-b";
      if (kind === "session") auth.version += 1;
      if (kind === "pin") auth.pinGeneration += 1;
      view.rerenderPage();
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      expect(screen.getByLabelText("Title contains")).toHaveValue("");
      expect(writes()).toHaveLength(0);
      if (kind === "profile")
        expect(await screen.findByText("No series recording rules yet.")).toBeInTheDocument();
    },
  );

  it("rejects a previously captured write before sending it for a different profile", async () => {
    const client = new QueryClient();
    const hook = renderHook(() => useCreateLiveTVSeriesRule(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    });
    auth.profileId = "profile-b";
    await act(async () => {
      await expect(
        hook.result.current.mutateAsync({ title_match: "News", new_only: false }),
      ).rejects.toMatchObject({ name: "StaleApiRequestContextError" });
    });
    expect(mockRequest).not.toHaveBeenCalled();
  });

  it("does not publish a late response from the previous profile", async () => {
    const pending = deferred<{ series_rules: LiveTVSeriesRule[] }>();
    mockRequest.mockReturnValueOnce(pending.promise);
    const view = setup();
    await waitFor(() => expect(mockRequest).toHaveBeenCalledOnce());
    auth.profileId = "profile-b";
    view.rerenderPage();
    await screen.findByText("No series recording rules yet.");
    await act(async () => pending.resolve({ series_rules: [initialRule] }));
    expect(screen.queryByText("Evening News")).not.toBeInTheDocument();
  });
});
