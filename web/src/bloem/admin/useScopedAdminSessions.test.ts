import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const read = (name: string) => readFileSync(fileURLToPath(new URL(name, import.meta.url)), "utf8");

/** The body of `export function <name>(...) {...}` up to its closing brace at the line start. */
function functionBody(source: string, name: string) {
  const start = source.indexOf(`export function ${name}(`);
  expect(start, `${name} not found`).toBeGreaterThanOrEqual(0);
  const open = source.indexOf("{", source.indexOf(")", start));
  const end = source.indexOf("\n}", open);
  return source.slice(open, end + 2);
}

describe("useScopedAdminSessions", () => {
  it("is Silo's useAdminSessions plus the enabled flag", () => {
    const silo = functionBody(read("../../hooks/queries/admin/stats.ts"), "useAdminSessions")
      .replace("staleTime: ADMIN_STALE_TIME", "staleTime: 30_000")
      .replace("enabled: context !== null", "enabled: enabled && context !== null");
    const bloem = functionBody(read("./useScopedAdminSessions.ts"), "useScopedAdminSessions");
    expect(bloem).toBe(silo);
    expect(read("../../hooks/queries/admin/stats.ts")).toContain(
      "const ADMIN_STALE_TIME = 30_000;",
    );
  });
});
