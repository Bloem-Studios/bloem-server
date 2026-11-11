import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import { PreviewModeContext } from "./usePreviewMode";
import BloemLibraryRecommended from "./adapters/BloemLibraryRecommended";

// The audiobook library's own opening page has a personal continue-listening
// hero; a preview gets Silo's library rows instead.
vi.mock("@/pages/LibraryRecommended", () => ({
  default: ({ libraryId }: { libraryId: number }) => <p>Library rows {libraryId}</p>,
}));

const presentation = {
  kind: "guest" as const,
  ends_at: null,
  cta_url: "https://store.test/trial",
  cta_label: "Subscribe",
  item_cta_label: "",
  cta_message: "",
  subscribe_label: "",
  subscribe_url: "",
  storefront_url: "",
  launch_url: "",
};

it("opens an audiobook library on its rows for a preview", () => {
  render(
    <MemoryRouter initialEntries={["/library/28"]}>
      <PreviewModeContext.Provider value={presentation}>
        <BloemLibraryRecommended libraryId={28} libraryType="audiobooks" />
      </PreviewModeContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByText("Library rows 28")).toBeInTheDocument();
});
