import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import type { ItemDetail } from "@/api/types";
import EbookContent from "./adapters/EbookContent";
import AudiobookContent from "./adapters/AudiobookContent";
import MangaContent from "./adapters/MangaContent";
import { PreviewModeContext } from "./usePreviewMode";
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ user: { id: 1 }, profile: null }),
  useOptionalAuth: () => null,
}));
it.each(["ebook", "audiobook", "manga"] as const)(
  "keeps %s descriptions while removing personal and content controls",
  (type) => {
    const item = {
      type,
      content_id: "book",
      title: "A Book",
      overview: "A description",
      genres: ["Mystery"],
      versions: [],
      studios: [],
      cast: [],
      crew: [],
      manga: { chapters: [{ content_id: "chapter", title: "Chapter One" }] },
    } as unknown as ItemDetail;
    const Content =
      type === "ebook" ? EbookContent : type === "audiobook" ? AudiobookContent : MangaContent;
    render(
      <MemoryRouter>
        <QueryClientProvider
          client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
        >
          <PreviewModeContext.Provider
            value={{
              kind: "personal",
              ends_at: null,
              cta_url: "https://store.test/trial",
              cta_label: "Subscribe",
              item_cta_label: "",
              cta_message: "",
              subscribe_label: "",
              subscribe_url: "",
              storefront_url: "",
              launch_url: "",
            }}
          >
            <Content item={item as never} libraryId={25} />
          </PreviewModeContext.Provider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(screen.getByRole("link", { name: "Mystery" })).toHaveAttribute(
      "href",
      "/library/25?tab=library&genre=Mystery",
    );
    expect(screen.getByText("A description")).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: type === "audiobook" ? "Subscribe to listen" : "Subscribe to read",
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /mark|download|pin|start reading|listen from start|add to collection/i,
      }),
    ).not.toBeInTheDocument();
    if (type === "manga") expect(screen.getByText("Chapter One")).toBeInTheDocument();
  },
);
