// @vitest-environment jsdom

import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import LiveTV from "./LiveTV";
import type { LiveTVRecording } from "@/api/bloemTypes";

const state = vi.hoisted(() => ({
  allowed: true,
  recordings: [] as Partial<LiveTVRecording>[],
  blocked: false,
  pending: false,
  reload: vi.fn(),
  schedule: vi.fn(),
  cancel: vi.fn(),
}));
const ruleSurface = vi.hoisted(() => vi.fn());
vi.mock("@/components/livetv/LiveTVAccessGate", () => ({
  LiveTVAccessGate: ({ children }: { children: ReactNode }) =>
    state.allowed ? children : <p>Access denied</p>,
}));
vi.mock("@/components/livetv/LiveTVSeriesRules", () => ({
  LiveTVSeriesRules: () => {
    ruleSurface();
    return <p>Recording rule workflow</p>;
  },
}));
vi.mock("@/components/livetv/LiveTVGuideGrid", () => ({
  LiveTVGuideGrid: ({
    recordDisabled,
    onRecord,
  }: {
    recordDisabled: boolean;
    onRecord: (id: string) => void;
  }) => (
    <button disabled={recordDisabled} onClick={() => onRecord("program-1")}>
      Record guide program
    </button>
  ),
}));
vi.mock("@/components/livetv/ManualRecordingForm", () => ({ ManualRecordingForm: () => null }));
vi.mock("@/hooks/queries/useLiveTV", () => ({
  useLiveTVChannels: () => ({ data: [], isLoading: false, isError: false }),
  useLiveTVGuide: () => ({ data: { programs: [] }, isLoading: false }),
  useLiveTVRecordings: () => ({ data: state.recordings, isLoading: false }),
  useScheduleLiveTVRecording: () => ({
    mutate: state.schedule,
    isPending: state.pending,
    isBlocked: state.blocked,
    needsReload: state.blocked,
    reloadRecordings: state.reload,
  }),
  useCancelLiveTVRecording: () => ({
    mutate: state.cancel,
    isPending: state.pending,
    isBlocked: state.blocked,
  }),
}));

function Location() {
  return <output data-testid="location">{useLocation().search}</output>;
}
function setup(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <LiveTV />
      <Location />
    </MemoryRouter>,
  );
}
beforeEach(() => {
  state.allowed = true;
  state.recordings = [];
  state.blocked = false;
  state.pending = false;
  state.reload.mockReset().mockResolvedValue(undefined);
  state.schedule.mockReset();
  state.cancel.mockReset();
  ruleSurface.mockClear();
});
afterEach(cleanup);

describe("Live TV recording rule navigation", () => {
  it("opens the rule deep link even when no channels remain", () => {
    setup("/livetv?tab=series-rules");
    expect(screen.getByRole("tab", { name: "Recording rules" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Recording rule workflow")).toBeInTheDocument();
    expect(screen.getByText(/No Live TV channels yet/)).toBeInTheDocument();
  });

  it("allows keyboard navigation to the rules and retains other URL parameters", () => {
    setup("/livetv?channel=news");
    fireEvent.keyDown(screen.getByRole("tab", { name: "Recording rules" }), { key: "Enter" });
    expect(screen.getByText("Recording rule workflow")).toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent("channel=news&tab=series-rules");
  });

  it("links imported recordings and explains the unlinked recording boundary", () => {
    const recording = {
      id: "linked",
      channel_id: "news",
      title: "Recorded show",
      status: "completed",
      start: "2026-08-13T08:00:00Z",
      stop: "2026-08-13T09:00:00Z",
    };
    state.recordings = [
      { ...recording, library_item_id: "movie:acceptance" },
      { ...recording, id: "unlinked", title: "Unlinked show" },
    ];
    setup("/livetv?tab=recordings");
    expect(screen.getByRole("link", { name: "Open recording" })).toHaveAttribute(
      "href",
      "/item/movie%3Aacceptance",
    );
    expect(screen.getByText(/Automatic recording import is not available/)).toBeInTheDocument();
  });

  it("does not mount recording rules when the access gate denies the viewer", () => {
    state.allowed = false;
    setup("/livetv?tab=series-rules");
    expect(screen.getByText("Access denied")).toBeInTheDocument();
    expect(ruleSurface).not.toHaveBeenCalled();
  });
});

it("blocks guide scheduling after failed readback and offers reload without leaving the guide", async () => {
  state.blocked = true;
  setup("/livetv");
  const record = screen.getByRole("button", { name: "Record guide program" });
  expect(record).toBeDisabled();
  expect(screen.getByText(/Recording status is unknown/)).toBeInTheDocument();
  const reload = screen.getByRole("button", { name: "Reload recordings" });
  state.reload.mockRejectedValueOnce(new Error("Readback unavailable"));
  fireEvent.click(record);
  fireEvent.click(reload);
  await waitFor(() => expect(state.reload).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("tab", { name: "Guide" })).toHaveAttribute("aria-selected", "true");
  fireEvent.click(reload);
  await waitFor(() =>
    expect(screen.getByRole("tab", { name: /My recordings/ })).toHaveAttribute(
      "aria-selected",
      "true",
    ),
  );
  expect(state.schedule).not.toHaveBeenCalled();
});

it("blocks cancellation with an actionable recovery when no request is pending", () => {
  state.blocked = true;
  state.recordings = [{ id: "recording-1", status: "scheduled", title: "News" }];
  setup("/livetv?tab=recordings");
  const cancel = screen.getByRole("button", { name: "Cancel" });
  expect(cancel).toBeDisabled();
  expect(screen.getByRole("button", { name: "Reload recordings" })).toBeEnabled();
  fireEvent.click(cancel);
  expect(state.cancel).not.toHaveBeenCalled();
});
