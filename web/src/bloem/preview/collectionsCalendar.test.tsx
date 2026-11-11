import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import PreviewCollections from "./PreviewCollections";
import { PreviewCalendarDefault } from "./adapters/Calendar";
import { PreviewModeContext } from "./usePreviewMode";

vi.mock("@/api/v2/request", () => ({
  v2: vi.fn(async () => ({
    libraries: [
      {
        library_id: "1",
        collections: [
          {
            id: "c1",
            title: "Best of 2023",
            item_count: 92,
            poster_url: "https://img.test/c1.webp",
          },
          { id: "c2", title: "No Poster", item_count: 3 },
        ],
      },
    ],
  })),
}));

const guest = {
  kind: "guest" as const,
  ends_at: null,
  cta_url: "",
  cta_label: "",
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

it("shows each collection's poster on the preview's Collections page", async () => {
  const { container } = render(
    <MemoryRouter>
      <QueryClientProvider client={new QueryClient()}>
        <PreviewCollections />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  expect(await screen.findByRole("heading", { name: "Best of 2023" })).toBeInTheDocument();
  const posters = container.querySelectorAll("img");
  expect(posters).toHaveLength(1);
  expect(posters[0]).toHaveAttribute("src", "https://img.test/c1.webp");
  expect(screen.getByRole("heading", { name: "No Poster" })).toBeInTheDocument();
});

it.each([
  [guest, "/calendar", "/calendar?filter=everything"],
  [guest, "/calendar?filter=trending", "/calendar?filter=trending"],
  [null, "/calendar", "/calendar"],
] as const)("opens the calendar where a guest has something to see", (mode, from, to) => {
  render(
    <MemoryRouter initialEntries={[from]}>
      <PreviewModeContext.Provider value={mode}>
        <Routes>
          <Route
            path="/calendar"
            element={
              <>
                <PreviewCalendarDefault>
                  <p>The calendar</p>
                </PreviewCalendarDefault>
                <Location />
              </>
            }
          />
        </Routes>
      </PreviewModeContext.Provider>
    </MemoryRouter>,
  );
  expect(screen.getByRole("status", { name: "location" })).toHaveTextContent(to);
  expect(screen.getByText("The calendar")).toBeInTheDocument();
});
