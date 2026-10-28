import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
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
  subscribe_label: "",
  subscribe_url: "",
  storefront_url: "",
  launch_url: "",
};
function Location() {
  const location = useLocation();
  return <output aria-label="location">{location.pathname + location.search}</output>;
}
it.each([
  ["library", LibraryRecommended],
  ["audiobook", BloemLibraryRecommended],
] as const)("opens the %s library tab instead of the recommendations", (_, Tab) => {
  render(
    <MemoryRouter initialEntries={["/library/1?type=series"]}>
      <PreviewModeContext.Provider value={presentation}>
        <Routes>
          <Route
            path="/library/:id"
            element={
              <>
                <Tab libraryId={1} />
                <Location />
              </>
            }
          />
        </Routes>
      </PreviewModeContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByRole("status", { name: "location" })).toHaveTextContent(
    "/library/1?type=series&tab=library",
  );
  expect(
    screen.queryByRole("heading", {
      name: "Recommendations aren't available to preview members",
    }),
  ).not.toBeInTheDocument();
});
