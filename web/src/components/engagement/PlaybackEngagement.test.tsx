import { cleanup, render, screen, fireEvent, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { PlaybackSession } from "./PlaybackEngagement";
import { api } from "@/api/client";
vi.mock("@/api/client", () => ({ api: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});
it("renders without taking focus, accepts Right, and honors a configured twenty-second duration", async () => {
  vi.useFakeTimers();
  vi.mocked(api).mockResolvedValue({
    promotions: [
      {
        id: "offer",
        duration_seconds: 20,
        playback_style: "card",
        headline: "Autumn stories",
        image_url: "/image.png",
        expires_at: "2099-01-01T00:00:00Z",
        cta: { label: "Explore", url: "/collection" },
      },
    ],
  });
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const props = {
    scope: "test-session",
    contentId: "episode",
    position: 9,
    playing: true,
    seeking: false,
    blocked: false,
    chapters: [10],
  };
  const ui = (position: number) => (
    <QueryClientProvider client={cache}>
      <button>Player surface</button>
      <PlaybackSession {...props} position={position} />
    </QueryClientProvider>
  );
  const result = render(ui(9));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(500);
  });
  screen.getByText("Player surface").focus();
  result.rerender(ui(10));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(100);
  });
  expect(screen.getByText("Autumn stories")).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByText("Player surface"));
  // A player surface isn't a button in production; leave the unrelated control first.
  (document.activeElement as HTMLElement).blur();
  fireEvent.keyDown(document.body, { key: "ArrowRight" });
  expect(document.activeElement).toBe(screen.getByText("Send to my phone"));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(screen.queryByText("Autumn stories")).not.toBeNull();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(screen.queryByText("Autumn stories")).toBeNull();
});
