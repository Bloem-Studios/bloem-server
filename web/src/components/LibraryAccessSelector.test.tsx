// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { LibraryAccessSelector } from "./LibraryAccessSelector";

describe("LibraryAccessSelector", () => {
  afterEach(cleanup);

  it("gives the ceiling and every library switch an explicit accessible name", () => {
    render(
      <LibraryAccessSelector
        scopeLabel="North Sea Media"
        libraries={[
          { id: 4, name: "Family Movies", type: "movies", enabled: true },
          { id: 8, name: "Platform Series", type: "series", enabled: true },
        ]}
        value={[4]}
        onChange={vi.fn()}
      />,
    );

    expect(
      screen.getByRole("switch", { name: "Allow all North Sea Media libraries" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Allow Family Movies" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Allow Platform Series" })).not.toBeChecked();
  });
});
