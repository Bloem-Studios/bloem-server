// @vitest-environment jsdom
import type { PropsWithChildren } from "react";
import { toast, type Action } from "sonner";
import { onlineManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  captureProfileRequestContext,
  setAccessToken,
  setProfileId,
  setProfileToken,
} from "@/api/client";
import {
  useCancelLiveTVRecording,
  useLiveTVRecordings,
  useScheduleLiveTVRecording,
} from "./useLiveTV";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const recording = {
  id: "recording/1",
  channel_id: "news",
  program_id: "program-1",
  status: "scheduled",
  start: "2099-01-01T19:00:00Z",
  stop: "2099-01-01T20:00:00Z",
  title: "Evening News",
};
const response = (recordings = [recording]) =>
  new Response(JSON.stringify({ recordings }), { status: 200 });
const clients: QueryClient[] = [];
const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  setAccessToken("test-account");
  setProfileId("viewer-1");
  setProfileToken("test-proof");
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  cleanup();
  clients.splice(0).forEach((client) => client.clear());
  setAccessToken(null);
  setProfileId(null);
  setProfileToken(null);
  onlineManager.setOnline(true);
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
function mountRecordings(status?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: 3 } },
  });
  clients.push(client);
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return {
    client,
    ...renderHook(
      () => ({
        recordings: useLiveTVRecordings(status),
        schedule: useScheduleLiveTVRecording(),
        cancel: useCancelLiveTVRecording(),
      }),
      { wrapper },
    ),
  };
}
function writes() {
  return fetchMock.mock.calls.filter(
    ([, init]) => init?.method === "POST" || init?.method === "DELETE",
  );
}

describe.each(["schedule", "cancel"] as const)("guide %s reconciliation", (action) => {
  it.each(["success", "lost response"])(
    "awaits authoritative readback after %s",
    async (outcome) => {
      const readback = deferred<Response>();
      let reads = 0;
      fetchMock.mockImplementation(async (_url, init) => {
        if (init?.method === "POST" || init?.method === "DELETE") {
          if (outcome === "lost response") throw new TypeError("Response lost after commit");
          return new Response(JSON.stringify(recording));
        }
        return ++reads === 1 ? response([]) : readback.promise;
      });
      const view = mountRecordings();
      await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
      let settled = false;
      let operation!: Promise<unknown>;
      act(() => {
        operation = (
          action === "schedule"
            ? view.result.current.schedule.mutateAsync({ program_id: "program-1" })
            : view.result.current.cancel.mutateAsync("recording/1")
        )
          .catch((error: unknown) => error)
          .finally(() => {
            settled = true;
          });
      });
      await waitFor(() => expect(reads).toBe(2));
      expect(view.result.current[action].isPending).toBe(true);
      expect(settled).toBe(false);
      expect(writes()).toHaveLength(1);
      const [url, init] = writes()[0]!;
      expect(url).toBe(
        action === "schedule"
          ? "/api/bloem/v1/livetv/recordings"
          : "/api/bloem/v1/livetv/recordings/recording%2F1",
      );
      expect(new Headers(init?.headers).get("X-Profile-Id")).toBe("viewer-1");
      await act(async () => {
        readback.resolve(response());
        await operation;
      });
      await waitFor(() => expect(view.result.current[action].isPending).toBe(false));
      expect(view.result.current.recordings.data).toEqual([recording]);
      expect(writes()).toHaveLength(1);
    },
  );

  it("blocks another recording action until readback completes", async () => {
    const readback = deferred<Response>();
    let reads = 0;
    fetchMock.mockImplementation(async (_url, init) => {
      if (init?.method) return new Response(JSON.stringify(recording));
      return ++reads === 1 ? response([]) : readback.promise;
    });
    const view = mountRecordings();
    await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
    let operation!: Promise<unknown>;
    act(() => {
      operation =
        action === "schedule"
          ? view.result.current.schedule.mutateAsync({ program_id: "program-1" })
          : view.result.current.cancel.mutateAsync("recording/1");
    });
    await waitFor(() => expect(reads).toBe(2));
    await act(async () => {
      await (
        action === "schedule"
          ? view.result.current.cancel.mutateAsync("recording/2")
          : view.result.current.schedule.mutateAsync({ program_id: "program-2" })
      ).catch(() => {});
    });
    expect(writes()).toHaveLength(1);
    expect(view.result.current.schedule.isPending).toBe(true);
    expect(view.result.current.cancel.isPending).toBe(true);
    await act(async () => {
      readback.resolve(response());
      await operation;
    });
  });

  it("keeps failed readback blocked across hooks and remounts until explicit reload succeeds", async () => {
    let reads = 0;
    let readFailure = true;
    fetchMock.mockImplementation(async (_url, init) => {
      if (init?.method) throw new TypeError("Response lost after commit");
      if (++reads > 1 && readFailure) throw new TypeError("Readback unavailable");
      return response();
    });
    const view = mountRecordings();
    await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
    await act(async () => {
      await (
        action === "schedule"
          ? view.result.current.schedule.mutateAsync({ program_id: "program-1" })
          : view.result.current.cancel.mutateAsync(recording.id)
      ).catch(() => {});
    });
    await act(async () => {
      await view.result.current.schedule.mutateAsync({ program_id: "program-1" }).catch(() => {});
      await view.result.current.cancel.mutateAsync(recording.id).catch(() => {});
    });
    expect(writes()).toHaveLength(1);
    expect(view.result.current.schedule.isPending).toBe(false);
    expect(view.result.current.schedule.isBlocked).toBe(true);
    expect(view.result.current.cancel.needsReload).toBe(true);

    const client = view.client;
    view.unmount();
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const remounted = renderHook(useScheduleLiveTVRecording, { wrapper });
    expect(remounted.result.current.isBlocked).toBe(true);
    await act(async () => {
      await remounted.result.current.reloadRecordings().catch(() => {});
    });
    expect(remounted.result.current.isBlocked).toBe(true);

    readFailure = false;
    // A background refresh is not an explicit reconciliation acknowledgement.
    await act(async () => {
      await client.refetchQueries({ queryKey: ["livetv", "recordings"] });
    });
    expect(remounted.result.current.isBlocked).toBe(true);
    const readsBeforeReload = reads;
    await act(async () => {
      await remounted.result.current.reloadRecordings();
    });
    expect(reads).toBeGreaterThan(readsBeforeReload);
    expect(remounted.result.current.isBlocked).toBe(false);
    expect(writes()).toHaveLength(1);
    await act(async () => {
      await remounted.result.current.mutateAsync({ program_id: "program-2" }).catch(() => {});
    });
    expect(writes()).toHaveLength(2);
  });

  it("does not queue an offline write for a later connection", async () => {
    let reads = 0;
    fetchMock.mockImplementation(async (_url, init) => {
      if (init?.method) throw new TypeError("Offline");
      reads++;
      return response([]);
    });
    const view = mountRecordings();
    await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
    onlineManager.setOnline(false);
    await act(async () => {
      await (
        action === "schedule"
          ? view.result.current.schedule.mutateAsync({ program_id: "program-1" })
          : view.result.current.cancel.mutateAsync("recording/1")
      ).catch(() => {});
    });
    expect(writes()).toHaveLength(1);
    expect(view.result.current[action].isPaused).toBe(false);
    expect(reads).toBe(2);
    onlineManager.setOnline(true);
    expect(writes()).toHaveLength(1);
  });

  it.each([
    ["profile", "success"],
    ["profile", "lost response"],
    ["proof", "success"],
    ["proof", "lost response"],
    ["account", "success"],
    ["account", "lost response"],
  ])(
    "does not rebind a pending write after %s replacement and %s",
    async (replacement, outcome) => {
      const write = deferred<Response>();
      fetchMock.mockImplementation(async (_url, init) =>
        init?.method ? write.promise : response([]),
      );
      const view = mountRecordings();
      await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
      let operation!: Promise<unknown>;
      act(() => {
        operation = (
          action === "schedule"
            ? view.result.current.schedule.mutateAsync({ program_id: "program-1" })
            : view.result.current.cancel.mutateAsync("recording/1")
        ).catch((error: unknown) => error);
      });
      await waitFor(() => expect(writes()).toHaveLength(1));
      act(() => {
        if (replacement === "profile") setProfileId("viewer-2");
        else if (replacement === "proof") setProfileToken("test-proof");
        else setAccessToken("replacement-account");
        view.rerender();
      });
      await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
      const readsBeforeCompletion = fetchMock.mock.calls.filter(([, init]) => !init?.method).length;
      await act(async () => {
        if (outcome === "success") write.resolve(new Response(JSON.stringify(recording)));
        else write.reject(new TypeError("Response lost"));
        await operation;
      });
      expect(fetchMock.mock.calls.filter(([, init]) => !init?.method)).toHaveLength(
        readsBeforeCompletion,
      );
      expect(writes()).toHaveLength(1);
      expect(view.result.current.recordings.data).toEqual([]);
    },
  );
});

it("starts readback after the write instead of joining a pre-write list fetch", async () => {
  const initialRead = deferred<Response>();
  let reads = 0;
  fetchMock.mockImplementation(async (_url, init) => {
    if (init?.method) return new Response(JSON.stringify(recording));
    return ++reads === 1 ? initialRead.promise : response();
  });
  const view = mountRecordings();
  await waitFor(() => expect(reads).toBe(1));
  let operation!: Promise<unknown>;
  act(() => {
    operation = view.result.current.schedule
      .mutateAsync({ program_id: "program-1" })
      .catch((error: unknown) => error);
  });
  await waitFor(() => expect(reads).toBe(2));
  await act(async () => {
    await operation;
    initialRead.resolve(response([]));
  });
  expect(view.result.current.recordings.data).toEqual([recording]);
});

it("discards a readback response after profile authority changes", async () => {
  const oldReadback = deferred<Response>();
  let reads = 0;
  fetchMock.mockImplementation(async (_url, init) => {
    if (init?.method) return new Response(JSON.stringify(recording));
    reads++;
    return reads === 2 ? oldReadback.promise : response([]);
  });
  const view = mountRecordings();
  await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
  let operation!: Promise<unknown>;
  act(() => {
    operation = view.result.current.schedule
      .mutateAsync({ program_id: "program-1" })
      .catch((error: unknown) => error);
  });
  await waitFor(() => expect(reads).toBe(2));
  act(() => {
    setProfileId("viewer-2");
    view.rerender();
  });
  await waitFor(() => expect(reads).toBe(3));
  await waitFor(() => expect(view.result.current.recordings.data).toEqual([]));
  await act(async () => {
    oldReadback.resolve(response());
    await operation;
  });
  expect(view.result.current.recordings.data).toEqual([]);
  expect(
    view.client
      .getQueryCache()
      .getAll()
      .every((query) => !JSON.stringify(query.state.data).includes("Evening News")),
  ).toBe(true);
  expect(reads).toBe(3);
});

it("performs readback when no recordings list is mounted", async () => {
  fetchMock.mockImplementation(async (_url, init) =>
    init?.method ? new Response(JSON.stringify(recording)) : response(),
  );
  const client = new QueryClient();
  clients.push(client);
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const view = renderHook(useScheduleLiveTVRecording, { wrapper });
  await act(async () => {
    await view.result.current.mutateAsync({ program_id: "program-1" });
  });
  expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method ?? "GET"])).toEqual([
    ["/api/bloem/v1/livetv/recordings", "POST"],
    ["/api/bloem/v1/livetv/recordings", "GET"],
  ]);
  expect(
    client
      .getQueryCache()
      .getAll()
      .map((query) => query.state.data),
  ).toEqual([[recording]]);
});

it("keeps cancellation pending until the active filtered list reconciles", async () => {
  const filteredReadback = deferred<Response>();
  let filteredReads = 0;
  fetchMock.mockImplementation(async (url, init) => {
    if (init?.method) return new Response(JSON.stringify({ ...recording, status: "cancelled" }));
    if (String(url).includes("?status=scheduled")) {
      return ++filteredReads === 1 ? response() : filteredReadback.promise;
    }
    return response([{ ...recording, status: "cancelled" }]);
  });
  const view = mountRecordings("scheduled");
  await waitFor(() => expect(view.result.current.recordings.data).toEqual([recording]));
  let operation!: Promise<unknown>;
  act(() => {
    operation = view.result.current.cancel
      .mutateAsync(recording.id)
      .catch((error: unknown) => error);
  });
  await waitFor(() => expect(filteredReads).toBe(2));
  expect(view.result.current.cancel.isPending).toBe(true);
  await act(async () => {
    filteredReadback.resolve(response([]));
    await operation;
  });
  expect(
    view.client
      .getQueryCache()
      .getAll()
      .find((query) => query.queryKey.at(-1) === "scheduled")?.state.data,
  ).toEqual([]);
  await waitFor(() => expect(view.result.current.recordings.data).toEqual([]));
  expect(writes()).toHaveLength(1);
});

it("fences an explicit reload and its result when the profile changes", async () => {
  const reload = deferred<Response>();
  let reads = 0;
  fetchMock.mockImplementation(async (_url, init) => {
    if (init?.method) throw new TypeError("Response lost");
    reads++;
    if (reads === 2)
      return new Response(JSON.stringify({ error: "Readback unavailable" }), { status: 500 });
    if (reads === 3) return reload.promise;
    return response([]);
  });
  const view = mountRecordings();
  await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
  await act(async () => {
    await view.result.current.schedule.mutateAsync({ program_id: "program-1" }).catch(() => {});
  });
  expect(view.result.current.schedule.isBlocked).toBe(true);
  const oldReload = view.result.current.schedule.reloadRecordings;
  let operation!: Promise<unknown>;
  act(() => {
    operation = oldReload().catch((error: unknown) => error);
  });
  await waitFor(() => expect(reads).toBe(3));
  await act(async () => {
    await view.result.current.cancel.mutateAsync(recording.id).catch(() => {});
  });
  expect(writes()).toHaveLength(1);
  act(() => {
    setProfileId("viewer-2");
    view.rerender();
  });
  await waitFor(() => expect(view.result.current.recordings.data).toEqual([]));
  expect(view.result.current.schedule.isBlocked).toBe(false);
  await act(async () => {
    reload.resolve(response());
    await operation;
    await oldReload().catch(() => {});
  });
  expect(reads).toBe(4);
  expect(view.result.current.recordings.data).toEqual([]);
  expect(
    view.client
      .getQueryCache()
      .getAll()
      .every((query) => !JSON.stringify(query.state.data).includes("Evening News")),
  ).toBe(true);
  expect(writes()).toHaveLength(1);
  act(() => {
    setProfileId("viewer-1");
    view.rerender();
  });
  expect(view.result.current.schedule.isBlocked).toBe(true);
});

it.each(["profile", "proof", "account"])(
  "never recaptures a supplied manual draft after %s replacement",
  async (replacement) => {
    const authority = captureProfileRequestContext()!;
    const client = new QueryClient();
    clients.push(client);
    const wrapper = ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const view = renderHook(() => useScheduleLiveTVRecording(authority), { wrapper });
    act(() => {
      if (replacement === "profile") setProfileId("viewer-2");
      else if (replacement === "proof") setProfileToken("test-proof");
      else setAccessToken("replacement-account");
      view.rerender();
    });
    await act(async () => {
      await expect(
        view.result.current.mutateAsync({ channel_id: "news", title: "Old draft" }),
      ).rejects.toThrow();
      await expect(view.result.current.reloadRecordings()).rejects.toThrow();
    });
    expect(fetchMock).not.toHaveBeenCalled();
  },
);

it("requires successful filtered readback before unlocking explicit recovery", async () => {
  let filteredReads = 0;
  const filteredRecovery = deferred<Response>();
  fetchMock.mockImplementation(async (url, init) => {
    if (init?.method) return new Response(JSON.stringify(recording));
    if (String(url).includes("?status=scheduled")) {
      filteredReads++;
      if (filteredReads === 1) return response();
      if (filteredReads === 2) return new Response("unavailable", { status: 500 });
      return filteredRecovery.promise;
    }
    return response([{ ...recording, status: "cancelled" }]);
  });
  const view = mountRecordings("scheduled");
  await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
  await act(async () => {
    await view.result.current.cancel.mutateAsync(recording.id).catch(() => {});
  });
  expect(view.result.current.cancel.needsReload).toBe(true);
  expect(view.result.current.schedule.isBlocked).toBe(true);
  let reload!: Promise<void>;
  act(() => {
    reload = view.result.current.schedule.reloadRecordings();
  });
  await waitFor(() => expect(filteredReads).toBe(3));
  expect(view.result.current.cancel.isBlocked).toBe(true);
  expect(view.result.current.cancel.isPending).toBe(true);
  await act(async () => {
    await view.result.current.cancel.mutateAsync(recording.id).catch(() => {});
    filteredRecovery.resolve(response([]));
    await reload;
  });
  expect(writes()).toHaveLength(1);
  expect(view.result.current.cancel.isBlocked).toBe(false);
  await waitFor(() => expect(view.result.current.recordings.data).toEqual([]));
});

it("offers reload directly from cancellation error feedback for consumers without recovery controls", async () => {
  let failReads = false;
  fetchMock.mockImplementation(async (_url, init) => {
    if (init?.method) {
      failReads = true;
      throw new TypeError("Response lost");
    }
    if (failReads) return new Response("unavailable", { status: 500 });
    return response();
  });
  const view = mountRecordings();
  await waitFor(() => expect(view.result.current.recordings.isSuccess).toBe(true));
  await act(async () => {
    await view.result.current.cancel.mutateAsync(recording.id).catch(() => {});
  });
  expect(view.result.current.cancel.isBlocked).toBe(true);
  const options = vi.mocked(toast.error).mock.calls.at(-1)?.[1];
  expect(options?.action).toMatchObject({ label: "Reload recordings" });
  const action = options?.action as Action;
  const feedback = render(<button onClick={action.onClick}>{action.label}</button>);
  failReads = false;
  fireEvent.click(feedback.getByRole("button", { name: "Reload recordings" }));
  await waitFor(() => expect(view.result.current.cancel.isBlocked).toBe(false));
  expect(writes()).toHaveLength(1);
});
