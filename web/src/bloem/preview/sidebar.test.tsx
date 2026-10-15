import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { expect, it, vi } from "vitest";
import PreviewSidebar from "./PreviewSidebar";
vi.mock("@/hooks/queries/libraries", () => ({
  useUserLibraries: () => ({ data: [{ id: 25, name: "Series", type: "series", sort_order: 0 }] }),
}));
it("keeps catalogue navigation and omits restricted destinations", () => {
  render(
    <MemoryRouter>
      <PreviewSidebar />
    </MemoryRouter>,
  );
  expect(screen.getByRole("link", { name: "Series" })).toHaveAttribute("href", "/library/25");
  expect(screen.getByRole("link", { name: "Home" })).toHaveAttribute("href", "/");
  expect(screen.getByRole("link", { name: "Search" })).toHaveAttribute("href", "/catalog");
  expect(
    screen.queryByRole("link", {
      name: /requests|party|for you|settings|profile|notification|favorite/i,
    }),
  ).not.toBeInTheDocument();
});
