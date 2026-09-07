import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlayerConfigProvider, type PlayerConfig } from "../context/PlayerConfigContext";
import { usePlaybackRealtime } from "./usePlaybackRealtime";

const mocks = vi.hoisted(() => ({
  capture: vi.fn(),
  current: vi.fn(),
  durable: vi.fn(),
  mint: vi.fn(),
  terminated: vi.fn(() => false),
  terminate: vi.fn(),
}));
vi.mock("@/api/client", () => ({
  captureProfileRequestContext: mocks.capture,
  isCapturedProfileAuthorityActive: mocks.current,
  StaleApiRequestContextError: class extends Error {},
}));
vi.mock("../durable-session-mutations", () => ({
  hasDurableTermination: mocks.terminated,
  recordDurableTermination: mocks.terminate,
}));
vi.mock("../session-mutations", () => ({ durableSessionFor: mocks.durable }));
vi.mock("@/api/v2/playbackControlSocket", async (original) => ({
  ...(await original<object>()),
  mintPlaybackControlSocketTicket: mocks.mint,
}));
const config: PlayerConfig = {
  apiBaseUrl: "/api/v1",
  getAccessToken: () => "private-token",
  getProfileId: () => "profile",
  getDeviceId: () => "device",
};
const authority = { profileId: "profile" };
class Socket extends EventTarget {
  static OPEN = 1;
  static CONNECTING = 0;
  static instances: Socket[] = [];
  readyState = 0;
  send = vi.fn();
  close = vi.fn(() => {
    this.readyState = 3;
    this.dispatchEvent(new Event("close"));
  });
  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    super();
    Socket.instances.push(this);
  }
}
const wrapper = ({ children }: { children: React.ReactNode }) =>
  createElement(PlayerConfigProvider, { config, children });
beforeEach(() => {
  vi.clearAllMocks();
  Socket.instances = [];
  mocks.capture.mockReturnValue(authority);
  mocks.current.mockReturnValue(true);
  mocks.durable.mockReturnValue({
    identity: { installationId: "install", origin: "https://silo.example.test" },
    context: { isCurrent: () => mocks.current() },
  });
  mocks.mint.mockResolvedValue({ ticket: "ticket", protocol: "silo.playback-control.v2" });
  vi.stubGlobal("WebSocket", Socket);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
it("opens only installation-bound v2 control without a URL credential", async () => {
  renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }), { wrapper });
  await waitFor(() => expect(Socket.instances).toHaveLength(1));
  expect(mocks.mint).toHaveBeenCalledWith("session", "install", authority);
  expect(Socket.instances[0]!.url).toBe(
    "wss://silo.example.test/api/v2/playback/sessions/session/control/ws",
  );
  expect(Socket.instances[0]!.protocols).toEqual([
    "silo.playback-control.v2",
    "silo.ticket.ticket",
  ]);
});
it.each([404, 503, 403])("never opens bridge control after ticket failure %s", async (status) => {
  vi.useFakeTimers();
  mocks.mint.mockRejectedValue(new Error(String(status)));
  const onCommand = vi.fn();
  const { result } = renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand }), {
    wrapper,
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(510);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(2);
  expect(Socket.instances).toHaveLength(0);
  expect(result.current.connectionState).toBe("disconnected");
  expect(onCommand).not.toHaveBeenCalled();
});

it.each(["stop", "terminate"])(
  "delivers an explicit %s only for the captured session and acknowledges it once",
  async (name) => {
    const onCommand = vi.fn().mockResolvedValue(undefined);
    renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand }), { wrapper });
    await waitFor(() => expect(Socket.instances).toHaveLength(1));
    const socket = Socket.instances[0]!;
    socket.readyState = Socket.OPEN;
    act(() => socket.dispatchEvent(new Event("open")));
    socket.send.mockClear();
    const command = { type: "command", session_id: "session", command_id: "terminal", name };
    const receive = (value: typeof command) =>
      socket.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(value) }));

    await act(async () => {
      receive({ ...command, session_id: "another-session" });
    });
    expect(onCommand).not.toHaveBeenCalled();
    expect(socket.send).not.toHaveBeenCalled();

    await act(async () => {
      receive(command);
      receive(command);
    });
    expect(onCommand).toHaveBeenCalledExactlyOnceWith(expect.objectContaining(command));
    expect(socket.send.mock.calls.map(([data]) => JSON.parse(data))).toEqual([
      { type: "ack", session_id: "session", command_id: "terminal", status: "accepted" },
      { type: "result", session_id: "session", command_id: "terminal", status: "completed" },
    ]);

    mocks.current.mockReturnValue(false);
    await act(async () => receive({ ...command, command_id: "late-terminal" }));
    expect(onCommand).toHaveBeenCalledTimes(1);
  },
);
it("does not mint for missing durable or profile authority", () => {
  mocks.durable.mockReturnValue(undefined);
  const first = renderHook(
    () => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }),
    { wrapper },
  );
  first.unmount();
  mocks.capture.mockReturnValue(null);
  renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }), { wrapper });
  expect(mocks.mint).not.toHaveBeenCalled();
  expect(Socket.instances).toHaveLength(0);
});
it("does not reconnect an old session under a replacement account", async () => {
  vi.useFakeTimers();
  renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand: vi.fn() }), { wrapper });
  await act(async () => {
    await Promise.resolve();
  });
  expect(Socket.instances).toHaveLength(1);
  mocks.current.mockReturnValue(false);
  mocks.capture.mockReturnValue({ profileId: "replacement" });
  await act(async () => {
    Socket.instances[0]!.close();
    await vi.advanceTimersByTimeAsync(600);
  });
  expect(mocks.mint).toHaveBeenCalledTimes(1);
  expect(Socket.instances).toHaveLength(1);
});

it("records administrator revocation before exit cleanup but never records an ordinary stop", async () => {
  const onCommand = vi.fn((command) => {
    expect(mocks.terminate).toHaveBeenCalledTimes(command.name === "terminate" ? 1 : 0);
  });
  renderHook(() => usePlaybackRealtime({ sessionId: "session", onCommand }), { wrapper });
  await waitFor(() => expect(Socket.instances).toHaveLength(1));
  const socket = Socket.instances[0]!;
  socket.readyState = Socket.OPEN;
  act(() => socket.dispatchEvent(new Event("open")));
  for (const name of ["stop", "terminate"]) {
    await act(async () =>
      socket.dispatchEvent(
        new MessageEvent("message", {
          data: JSON.stringify({
            type: "command",
            session_id: "session",
            command_id: name,
            name,
            issued_by: { kind: "admin" },
          }),
        }),
      ),
    );
  }
  expect(mocks.terminate).toHaveBeenCalledExactlyOnceWith(
    config,
    mocks.durable.mock.results[mocks.durable.mock.results.length - 1]!.value,
    "terminate",
  );
});
