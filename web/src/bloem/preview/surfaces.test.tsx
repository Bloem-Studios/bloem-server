import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import type { SectionItem } from "@/api/types";
import { Tabs } from "@/components/ui/tabs";
import HeroBanner from "./adapters/HeroBanner";
import LibraryHeader from "./adapters/LibraryHeader";
import { PreviewCtaContext, PreviewModeContext } from "./usePreviewMode";
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ user: null }),
  useOptionalAuth: () => null,
}));
it("replaces hero playback while keeping its detail destination", async () => {
  const opened: string[] = [];
  render(
    <MemoryRouter>
      <QueryClientProvider client={new QueryClient()}>
        <PreviewModeContext.Provider
          value={{
            kind: "guest",
            ends_at: null,
            cta_url: "https://store.test/trial",
            cta_label: "Subscribe",
            item_cta_label: "",
            cta_message: "",
            storefront_url: "",
            launch_url: "",
          }}
        >
          <PreviewCtaContext.Provider value={(id) => opened.push(id ?? "")}>
            <HeroBanner
              items={[
                {
                  content_id: "movie",
                  title: "Movie",
                  overview: "Description",
                  type: "movie",
                } as SectionItem,
              ]}
            />
          </PreviewCtaContext.Provider>
        </PreviewModeContext.Provider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Subscribe to watch" }));
  expect(opened).toEqual(["movie"]);
  expect(screen.getByRole("link", { name: "Details" })).toHaveAttribute("href", "/item/movie");
  expect(screen.queryByRole("link", { name: /^play/i })).not.toBeInTheDocument();
});
it("removes library shuffle", () => {
  render(
    <MemoryRouter>
      <PreviewModeContext.Provider
        value={{
          kind: "personal",
          ends_at: null,
          cta_url: "",
          cta_label: "",
          item_cta_label: "",
          cta_message: "",
          storefront_url: "",
          launch_url: "",
        }}
      >
        <Tabs value="library">
          <LibraryHeader
            libraryName="Library"
            onShuffle={() => {
              throw new Error("mutation");
            }}
          />
        </Tabs>
      </PreviewModeContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.queryByRole("button", { name: "Shuffle" })).not.toBeInTheDocument();
});
