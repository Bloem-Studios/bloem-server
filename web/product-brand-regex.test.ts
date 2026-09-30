import { describe, expect, it } from "vitest";
import { brandTestRegexes } from "./product-brand-regex";

describe("brandTestRegexes", () => {
  it("adapts prose assertions including case-insensitive lower case", () => {
    expect(brandTestRegexes("const a = /Silo has not reviewed this plugin/; const b = /open in the silo app/i;")).toBe("const a = /Bloem has not reviewed this plugin/; const b = /open in the Bloem app/i;");
  });
  it("preserves protocol, path and identifier assertions", () => {
    const code = String.raw`const a = /silo:\/\//i; const b = /SiloBrand/; const c = /\/Silo/; const d = "silo://open";`;
    expect(brandTestRegexes(code)).toBe(code);
  });
});
