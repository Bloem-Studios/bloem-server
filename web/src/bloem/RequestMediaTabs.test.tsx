import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router";
import { describe, expect, it } from "vitest";
import { RequestMediaTabs } from "./RequestMediaTabs";

function Location() {
  const location = useLocation();
  return (
    <output>
      {location.pathname}
      {location.search}
    </output>
  );
}

describe("request media tabs", () => {
  it("keeps the route and unrelated query state while choosing ebooks or video", () => {
    render(
      <MemoryRouter initialEntries={["/admin/requests?filter=open"]}>
        <RequestMediaTabs ebooks />
        <Location />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Ebooks" }));
    expect(screen.getByText("/admin/requests?filter=open&kind=ebook")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Ebooks" })).toHaveAttribute("aria-current", "page");
    fireEvent.click(screen.getByRole("button", { name: "Movies & series" }));
    expect(screen.getByText("/admin/requests?filter=open")).toBeInTheDocument();
  });
  it("shows only advertised book types and defaults unknown kinds to video", () => {
    render(
      <MemoryRouter initialEntries={["/requests?kind=other"]}>
        <RequestMediaTabs audiobooks={false} ebooks />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("button", { name: "Audiobooks" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Movies & series" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });
});
