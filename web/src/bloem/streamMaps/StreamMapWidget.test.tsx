import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { adminV2Api } from "@/api/adminV2Client";
import { loadBloemAdminEnglishForTests } from "@/bloem/i18n";
import type { WidgetId } from "@/components/admin/dashboard/types";
import { WidgetChromeProvider } from "@/components/admin/dashboard/widgetChrome";
import { streamMapKeys } from "./queries";
import type { StreamInsightsCapabilities, StreamTrail, StreamTrails } from "./scene";
import type { StreamMapViewProps } from "./StreamMapWidget";
import { StreamFlatMapWidget, StreamGlobeWidget } from "./StreamMapWidget";

// The views draw with WebGL and Leaflet, which jsdom lacks; each stands in as
// the scene it was handed.
function FakeView({ scene, moving }: StreamMapViewProps) {
  return (
    <div data-testid="view" data-moving={String(moving)}>
      {scene.routes.map((route) => route.id).join(",")}
    </div>
  );
}
vi.mock("./StreamGlobe", () => ({ default: FakeView }));
vi.mock("./StreamFlatMap", () => ({ default: FakeView }));
vi.mock("./StreamSatellite", () => ({ default: FakeView }));

vi.mock("@/api/adminV2Client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/adminV2Client")>()),
  adminV2Api: vi.fn(),
}));
const api = vi.mocked(adminV2Api);

const TRAIL: StreamTrail = {
  session_id: "s1",
  live: true,
  transport: "udp",
  hops: [
    { kind: "viewer", label: "Amsterdam", lat: 52.37, lon: 4.9, approximate: false },
    { kind: "server", label: "Bloem", lat: 50.11, lon: 8.68, approximate: false },
  ],
};
const CAPABILITIES: StreamInsightsCapabilities = {
  available: true,
  locations: true,
  retention_days: 30,
  edge_header: "X-Bloem-Edge",
};

function client({
  capabilities = CAPABILITIES,
  trails,
}: {
  capabilities?: StreamInsightsCapabilities;
  trails?: Partial<Record<"live" | "24h" | "7d", StreamTrails>>;
}) {
  const queries = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  queries.setQueryData(streamMapKeys.capabilities, capabilities);
  for (const [window, data] of Object.entries(trails ?? {})) {
    queries.setQueryData(streamMapKeys.trails(window as "live"), data);
  }
  return queries;
}

function renderWidget(node: ReactNode, queries: QueryClient, view?: string, setView = vi.fn()) {
  render(
    <QueryClientProvider client={queries}>
      <WidgetChromeProvider
        id={"bloem-stream-globe" as WidgetId}
        range="day"
        setRange={() => {}}
        view={view}
        setView={(_id, next) => setView(next)}
      >
        {node}
      </WidgetChromeProvider>
    </QueryClientProvider>,
  );
  return setView;
}

beforeAll(async () => {
  await loadBloemAdminEnglishForTests();
});

beforeEach(() => {
  api.mockReset();
  // A read the tests did not seed fails, as an unreachable server would.
  api.mockRejectedValue(new Error("stream_insights_unavailable"));
});

describe("stream map widgets", () => {
  it("draws the live window's streams, moving, under the widget's title", async () => {
    renderWidget(
      <StreamGlobeWidget />,
      client({ trails: { live: { trails: [TRAIL], truncated: false } } }),
    );

    expect(screen.getByText("Stream globe")).toBeInTheDocument();
    expect(await screen.findByTestId("view")).toHaveTextContent("s1");
    expect(screen.getByTestId("view")).toHaveAttribute("data-moving", "true");
    expect(screen.getByText("1 stream")).toBeInTheDocument();
  });

  it("draws a past window still, from the stored view", async () => {
    renderWidget(
      <StreamFlatMapWidget />,
      client({ trails: { "7d": { trails: [{ ...TRAIL, live: false }], truncated: false } } }),
      "7d",
    );

    expect(screen.getByText("Stream map")).toBeInTheDocument();
    expect(await screen.findByTestId("view")).toHaveAttribute("data-moving", "false");
  });

  it("stores the window the admin picks as the widget's view", async () => {
    const setView = renderWidget(
      <StreamGlobeWidget />,
      client({ trails: { live: { trails: [], truncated: false } } }),
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Show the streams started in the last 24 hours" }),
    );

    expect(setView).toHaveBeenCalledWith("24h");
    expect(screen.getByRole("button", { name: "Show the streams playing now" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("says when nothing is playing, and lists the line colours", async () => {
    renderWidget(
      <StreamGlobeWidget />,
      client({ trails: { live: { trails: [], truncated: false } } }),
    );

    expect(await screen.findByText("Nothing is playing right now.")).toBeInTheDocument();
    const legend = screen.getByRole("list", { name: "Line colours" });
    expect(legend).toHaveTextContent("UDP");
    expect(legend).toHaveTextContent("TCP");
    expect(legend).toHaveTextContent("Cloudflare");
    expect(legend).toHaveTextContent("Unknown");
  });

  it("notes a server without locations and a window with more streams than listed", async () => {
    renderWidget(
      <StreamGlobeWidget />,
      client({
        capabilities: { ...CAPABILITIES, locations: false },
        trails: { live: { trails: [TRAIL], truncated: true } },
      }),
    );

    expect(
      await screen.findByText("Viewer locations are off: the server has no location database."),
    ).toBeInTheDocument();
    expect(screen.getByText("Showing the newest 1 streams.")).toBeInTheDocument();
  });

  it("says when the server's stream insights are down", async () => {
    renderWidget(
      <StreamGlobeWidget />,
      client({
        capabilities: { ...CAPABILITIES, available: false },
        trails: { live: { trails: [], truncated: false } },
      }),
    );

    expect(
      await screen.findByText("Stream routes aren't available on this server."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Nothing is playing right now.")).not.toBeInTheDocument();
  });

  it("says the read failed only when it has no routes to draw", async () => {
    renderWidget(<StreamGlobeWidget />, client({}));

    expect(await screen.findByText("Couldn't load the stream routes.")).toBeInTheDocument();
    expect(api).toHaveBeenCalledWith("/platform/streams/trails?window=live");
  });

  it("keeps drawing the last routes when a refresh fails", async () => {
    const queries = client({ trails: { live: { trails: [TRAIL], truncated: false } } });
    renderWidget(<StreamGlobeWidget />, queries);
    expect(await screen.findByTestId("view")).toHaveTextContent("s1");

    await act(() => queries.refetchQueries({ queryKey: streamMapKeys.trails("live") }));

    // The refresh did fail, and the card heard about it.
    expect(api).toHaveBeenCalledWith("/platform/streams/trails?window=live");
    expect(queries.getQueryState(streamMapKeys.trails("live"))?.status).toBe("error");
    // React Query tells its observers on a later tick; let the card hear it.
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(screen.getByTestId("view")).toHaveTextContent("s1");
    expect(screen.queryByText("Couldn't load the stream routes.")).not.toBeInTheDocument();
  });
});
