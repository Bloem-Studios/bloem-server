// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { nativeApiWithProfileRequestContext as apiWithProfileRequestContext } from "@/api/client";
import type { LiveTVChannel, LiveTVProgram } from "@/api/types";
import { ManualRecordingForm } from "./ManualRecordingForm";
import { LiveTVGuideGrid } from "./LiveTVGuideGrid";
import { LiveTVSeriesRules } from "./LiveTVSeriesRules";

const identity = vi.hoisted(() => ({ generation: 1 }));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ profile: { id: "viewer" } }) }));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureSessionIdentity: () => ({
    serverOrigin: "https://server.test",
    authContextVersion: identity.generation,
  }),
  isSessionIdentityCurrent: (scope: { authContextVersion: number }) =>
    scope.authContextVersion === identity.generation,
  captureProfileRequestContext: () => ({
    serverOrigin: "https://server.test",
    profileId: "viewer",
    authContextVersion: identity.generation,
    profileTokenGeneration: 1,
  }),
  isCapturedProfileAuthorityActive: (scope: { authContextVersion: number }) =>
    scope.authContextVersion === identity.generation,
  nativeApiWithProfileRequestContext: vi.fn(),
}));
const channel: LiveTVChannel = {
  id: "news",
  name: "News",
  number: "1",
  callsign: "NEWS",
  enabled: true,
  tuner_id: "tuner",
  logo_url: "",
  hd: true,
  stream_url: "",
  guide_station_id: "",
};
const program: LiveTVProgram = {
  id: "episode",
  channel_id: "news",
  series_id: "series-exact",
  title: "Evening News",
  description: "News",
  genres: [],
  subtitle: "Today",
  image_url: "",
  start: "2099-01-01T19:00:00Z",
  stop: "2099-01-01T20:00:00Z",
  is_new: true,
  is_live: false,
  season: null,
  episode: null,
};
const request = vi.mocked(apiWithProfileRequestContext);
beforeEach(() => {
  identity.generation = 1;
  request.mockReset();
  request.mockResolvedValue({ series_rules: [], recordings: [] });
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
function mountManual() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <ManualRecordingForm channels={[channel]} />
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, client, refresh: () => view.rerender(tree()) };
}
function fillManual(stop = "2099-01-01T20:00") {
  fireEvent.click(screen.getByText("Schedule by channel and time"));
  fireEvent.change(screen.getByLabelText("Recording title"), { target: { value: "Evening News" } });
  fireEvent.change(screen.getByLabelText("Recording channel"), { target: { value: "news" } });
  fireEvent.change(screen.getByLabelText("Starts at"), { target: { value: "2099-01-01T19:00" } });
  fireEvent.change(screen.getByLabelText("Ends at"), { target: { value: stop } });
}
it("schedules a manual recording with captured authority, ISO times and no replay", async () => {
  mountManual();
  fillManual();
  fireEvent.click(screen.getByRole("button", { name: "Schedule recording" }));
  await screen.findByText("Recording scheduled.");
  expect(request).toHaveBeenCalledTimes(2);
  expect(request).toHaveBeenCalledWith(
    "/livetv/recordings",
    expect.objectContaining({ authContextVersion: 1 }),
    expect.objectContaining({ method: "POST" }),
    "none",
  );
  const payload = JSON.parse(String(request.mock.calls[0]?.[2]?.body));
  expect(payload).toEqual({
    channel_id: "news",
    title: "Evening News",
    start: new Date("2099-01-01T19:00").toISOString(),
    stop: new Date("2099-01-01T20:00").toISOString(),
  });
  expect(request).toHaveBeenLastCalledWith(
    "/livetv/recordings",
    expect.objectContaining({ authContextVersion: 1 }),
    expect.objectContaining({ signal: expect.any(AbortSignal) }),
    "safe",
  );
});
it("rejects invalid manual intervals without writing", () => {
  mountManual();
  fillManual("2099-01-01T18:00");
  fireEvent.click(screen.getByRole("button", { name: "Schedule recording" }));
  expect(screen.getByRole("alert")).toHaveTextContent("end time after the start");
  expect(request).not.toHaveBeenCalled();
});
it("discards manual drafts on session changes", () => {
  const view = mountManual();
  fillManual();
  identity.generation++;
  view.refresh();
  expect(screen.getByLabelText("Recording title")).toHaveValue("");
  expect(request).not.toHaveBeenCalled();
});
it.each(["success", "lost response"])(
  "blocks manual resubmission during readback after %s",
  async (outcome) => {
    let finishReadback!: (value: unknown) => void;
    const readback = new Promise((resolve) => {
      finishReadback = resolve;
    });
    request.mockImplementation(async (_path, _scope, options) => {
      if (options?.method === "POST") {
        if (outcome === "lost response") throw new TypeError("Response lost after commit");
        return { id: "recording-1" };
      }
      return readback;
    });
    mountManual();
    fillManual();
    const submit = screen.getByRole("button", { name: "Schedule recording" });
    fireEvent.click(submit);
    await waitFor(() => expect(request.mock.calls.some((call) => !call[2]?.method)).toBe(true));
    expect(submit).toBeDisabled();
    fireEvent.submit(submit.closest("form")!);
    await act(async () => {});
    expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toHaveLength(1);
    await act(async () => {
      finishReadback({ recordings: [] });
    });
    if (outcome === "lost response") await waitFor(() => expect(submit).toBeEnabled());
    else await screen.findByText("Recording scheduled.");
  },
);

it("offers manual recovery without retrying the write and requires a reviewed new draft", async () => {
  let failRead = true;
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method === "POST") throw new TypeError("Response lost after commit");
    if (failRead) throw new TypeError("Readback unavailable");
    return { recordings: [] };
  });
  mountManual();
  fillManual();
  const submit = screen.getByRole("button", { name: "Schedule recording" });
  fireEvent.click(submit);
  const reload = await screen.findByRole("button", { name: "Reload recordings" });
  expect(submit).toBeDisabled();
  expect(screen.getByText(/Recording status is unknown/)).toBeInTheDocument();
  fireEvent.submit(submit.closest("form")!);
  fireEvent.click(reload);
  await screen.findByText(/Could not reload recordings/);
  expect(submit).toBeDisabled();
  expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toHaveLength(1);
  failRead = false;
  fireEvent.click(reload);
  await screen.findByText(/Review the list and enter a title/);
  expect(screen.getByLabelText("Recording title")).toHaveValue("");
  expect(screen.queryByText(/Recording status is unknown/)).not.toBeInTheDocument();
  expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toHaveLength(1);
  fireEvent.change(screen.getByLabelText("Recording title"), {
    target: { value: "New reviewed recording" },
  });
  fireEvent.click(submit);
  await waitFor(() =>
    expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toHaveLength(2),
  );
});

it("discards manual recovery results when the draft authority changes", async () => {
  let finishReload!: (value: unknown) => void;
  const reload = new Promise((resolve) => {
    finishReload = resolve;
  });
  let reads = 0;
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method === "POST") throw new TypeError("Response lost after commit");
    if (++reads === 1) throw new TypeError("Readback unavailable");
    return reload;
  });
  const view = mountManual();
  fillManual();
  fireEvent.click(screen.getByRole("button", { name: "Schedule recording" }));
  fireEvent.click(await screen.findByRole("button", { name: "Reload recordings" }));
  await waitFor(() => expect(reads).toBe(2));
  identity.generation++;
  view.refresh();
  await act(async () => {
    finishReload({ recordings: [{ title: "Old recording" }] });
  });
  expect(screen.getByLabelText("Recording title")).toHaveValue("");
  expect(screen.queryByText(/Recordings reloaded/)).not.toBeInTheDocument();
  expect(screen.queryByText(/Recording status is unknown/)).not.toBeInTheDocument();
  expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toHaveLength(1);
});

it("offers a guide series action using the original program identity", () => {
  const select = vi.fn();
  render(
    <LiveTVGuideGrid
      channels={[channel]}
      programs={[program]}
      selectedChannelId={null}
      now={new Date("2099-01-01T19:00:00Z")}
      onSelectChannel={vi.fn()}
      onWatch={vi.fn()}
      onRecord={vi.fn()}
      onRecordSeries={select}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Record series: Evening News" }));
  expect(select).toHaveBeenCalledWith(program);
});
it("creates an exact guide-series rule instead of a title substring rule", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <LiveTVSeriesRules channels={[channel]} programs={[program]} selectedProgram={program} />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(screen.getByRole("button", { name: "Create rule" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Create rule" }));
  await waitFor(() =>
    expect(request.mock.calls.some((call) => call[2]?.method === "POST")).toBe(true),
  );
  const write = request.mock.calls.find((call) => call[2]?.method === "POST")!;
  expect(JSON.parse(String(write[2]?.body))).toMatchObject({
    series_id: "series-exact",
    title_match: "",
    channel_id: "news",
  });
  expect(write[3]).toBe("none");
});
