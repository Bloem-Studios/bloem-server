import { describe, expect, it } from "vitest";

import { organizationLibraryOptions } from "./organizationLibraryOptions";

describe("organizationLibraryOptions", () => {
  it("offers owned and actively entitled libraries as enabled rows", () => {
    const rows = organizationLibraryOptions([
      { folder_id: 4, name: "Family Movies", type: "movies", access_kind: "owned" },
      {
        folder_id: 7,
        name: "Shared Shows",
        type: "tv",
        access_kind: "entitled",
        entitlement: { id: "e1", status: "active", security_revision: 1 },
      },
      {
        folder_id: 9,
        name: "Paused",
        type: "tv",
        access_kind: "entitled",
        entitlement: { id: "e2", status: "suspended", security_revision: 2 },
      },
      { folder_id: 11, name: "No Grant", type: "movies", access_kind: "entitled" },
    ]);

    expect(rows.map(({ id, name, type, enabled }) => ({ id, name, type, enabled }))).toEqual([
      { id: 4, name: "Family Movies", type: "movies", enabled: true },
      { id: 7, name: "Shared Shows", type: "tv", enabled: true },
      { id: 9, name: "Paused", type: "tv", enabled: false },
      { id: 11, name: "No Grant", type: "movies", enabled: false },
    ]);
  });
});
