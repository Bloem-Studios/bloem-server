import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { expect, it } from "vitest";
import { PreviewModeContext } from "./usePreviewMode";
import LibraryRecommended from "./adapters/LibraryRecommended";
import BloemLibraryRecommended from "./adapters/BloemLibraryRecommended";
// No mock of the original pages: the preview plugin redirects that import to
// the adapter under test. A preview visitor never reaches them anyway.
const presentation = {
  kind: "guest" as const,
  ends_at: null,
  cta_url: "https://store.test/trial",
  cta_label: "Subscribe",
  item_cta_label: "",
  cta_message: "",
  storefront_url: "",
  launch_url: "",
};
it.each([
  ["library", LibraryRecommended],
  ["audiobook", BloemLibraryRecommended],
] as const)("tells a preview visitor the %s recommendations are not offered", (_, Tab) => {
  render(
    <MemoryRouter>
      <PreviewModeContext.Provider value={presentation}>
        <Tab libraryId={1} />
      </PreviewModeContext.Provider>
    </MemoryRouter>,
  );
  expect(
    screen.getByRole("heading", {
      name: "Recommendations aren't available to preview members",
    }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
});
