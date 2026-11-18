// @vitest-environment node
// Bloem: a library on a storage source reports it on the v2 wire (libraries.ts);
// new storage libraries are created through Bloem's library locations.
import { describe, expect, it } from "vitest";

import { libraryCreateToV2, libraryFromV2 } from "./libraries";

const source = "413183ec-ece5-4805-aac4-6c31f6ddf0df";

describe("storage source libraries on the v2 wire", () => {
  it("creates a folder library without a storage source", () => {
    const body = libraryCreateToV2({ name: "Movies", type: "movies", paths: ["/media"] });
    expect(body.paths).toEqual(["/media"]);
    expect(body).not.toHaveProperty("storage_source");
  });

  it("reads a library's storage source", () => {
    const library = { id: 3, paths: [], storage_source: source } as unknown as Parameters<
      typeof libraryFromV2
    >[0];
    expect(libraryFromV2(library).storage_source).toBe(source);
  });
});
