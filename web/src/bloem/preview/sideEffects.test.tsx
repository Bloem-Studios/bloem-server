import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { PreviewModeContext } from "./usePreviewMode";
import { useMetadataAIStatus } from "./adapters/metadataAI";
import { useSearchMediaScope } from "./adapters/searchScope";
const presentation = {
  kind: "personal" as const,
  ends_at: null,
  cta_url: "",
  cta_label: "",
  item_cta_label: "",
  cta_message: "",
  storefront_url: "",
  launch_url: "",
};
afterEach(() => vi.unstubAllGlobals());
function Status({ enabled }: { enabled: boolean }) {
  const status = useMetadataAIStatus(enabled);
  return <p>{status.isFetching ? "fetching" : "idle"}</p>;
}
it("keeps hook order stable when a metadata probe toggles, and makes no preview request", () => {
  const requests: unknown[] = [];
  vi.stubGlobal("fetch", async (url: unknown) => {
    requests.push(url);
    return Response.json({});
  });
  const client = new QueryClient();
  const view = render(
    <QueryClientProvider client={client}>
      <PreviewModeContext.Provider value={presentation}>
        <Status enabled={false} />
      </PreviewModeContext.Provider>
    </QueryClientProvider>,
  );
  view.rerender(
    <QueryClientProvider client={client}>
      <PreviewModeContext.Provider value={presentation}>
        <Status enabled />
      </PreviewModeContext.Provider>
    </QueryClientProvider>,
  );
  expect(screen.getByText("idle")).toBeInTheDocument();
  expect(requests).toEqual([]);
});
function Scope() {
  const scope = useSearchMediaScope();
  return (
    <>
      <p>Scope: {scope.scope}</p>
      <button onClick={() => scope.setScope("all")}>All</button>
    </>
  );
}
it("changes search scope within the preview without saving a preference", () => {
  const requests: unknown[] = [];
  vi.stubGlobal("fetch", async (url: unknown) => {
    requests.push(url);
    return Response.json({});
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <PreviewModeContext.Provider value={presentation}>
        <Scope />
      </PreviewModeContext.Provider>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("All"));
  expect(screen.getByText("Scope: all")).toBeInTheDocument();
  expect(requests).toEqual([]);
});
