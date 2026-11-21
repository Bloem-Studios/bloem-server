// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { LibraryAccessScope } from "@/bloem/libraryAccessScope";
import { organizationLibraryOptions } from "@/bloem/organizationLibraryOptions";

import { LibraryAccessSelector } from "./LibraryAccessSelector";

describe("LibraryAccessSelector", () => {
  afterEach(cleanup);

  it("gives the ceiling and every library switch an explicit accessible name", () => {
    render(
      <LibraryAccessScope name="North Sea Media">
        <LibraryAccessSelector
          libraries={organizationLibraryOptions([
            { folder_id: 4, name: "Family Movies", type: "movies", access_kind: "owned" },
            { folder_id: 8, name: "Platform Series", type: "series", access_kind: "owned" },
          ])}
          value={[4]}
          onChange={vi.fn()}
        />
      </LibraryAccessScope>,
    );

    expect(screen.getByRole("switch", { name: "All libraries" })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Allow Family Movies" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Allow Platform Series" })).not.toBeChecked();
  });
});
