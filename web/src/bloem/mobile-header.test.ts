// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// jsdom applies no stylesheets, so the test reads the sources as text: it
// checks that Silo's layout loads Bloem's header rule and what the rule keeps.
const read = (name: string) => readFileSync(fileURLToPath(new URL(name, import.meta.url)), "utf8");
const layoutSource = read("../components/Layout.tsx");
const sidebarAdapterSource = read("./preview/adapters/AppSidebar.tsx");
const previewPluginSource = read("../../bloem-preview-plugin.ts");
const headerCss = read("./mobile-header.css");

function declarations(css: string, selector: string): string {
  const start = css.indexOf(`${selector} {`);
  expect(start, `${selector} rule`).toBeGreaterThanOrEqual(0);
  return css.slice(start, css.indexOf("}", start)).replace(/\s+/g, " ");
}

describe("Bloem's phone header", () => {
  it("is loaded by Silo's layout through its sidebar adapter; Layout stays as Silo wrote it", () => {
    expect(previewPluginSource).toContain(
      '"components/Layout.tsx": { "components/AppSidebar.tsx": "adapters/AppSidebar.tsx" }',
    );
    expect(layoutSource).toMatch(/import AppSidebar from "@\/components\/AppSidebar";/);
    expect(sidebarAdapterSource).toContain('import "@/bloem/mobile-header.css";');
    expect(layoutSource).toContain("mobile-header glass-dark");
  });

  it("is an opaque bar across the full width that clears the safe areas", () => {
    // Unlayered, so it wins over Silo's Tailwind utilities and glass-dark.
    expect(headerCss).not.toMatch(/@layer/);
    const rule = declarations(headerCss, ".mobile-header[data-sticky-header]");
    expect(rule).toContain("margin: 0;");
    expect(rule).toContain("border-radius: 0;");
    expect(rule).toContain("border-width: 0 0 1px;");
    expect(rule).toContain("background: var(--background);");
    expect(rule).toContain("backdrop-filter: none;");
    expect(rule).toContain("box-shadow: none;");
    expect(rule).toContain("padding-top: env(safe-area-inset-top);");
    expect(rule).toContain("padding-right: max(0.5rem, env(safe-area-inset-right));");
    expect(rule).toContain("padding-left: max(0.5rem, env(safe-area-inset-left));");
  });

  it("packs the controls and shrinks the avatar to fit a narrow phone", () => {
    expect(declarations(headerCss, ".mobile-header[data-sticky-header] > div")).toContain(
      "gap: 0.25rem;",
    );
    const avatar = declarations(
      headerCss,
      '.mobile-header[data-sticky-header] [data-slot="avatar"]',
    );
    expect(avatar).toContain("width: 2rem;");
    expect(avatar).toContain("height: 2rem;");
    expect(avatar).toContain("box-shadow: none;");
  });
});

describe("Bloem's sheet overlay", () => {
  it("darkens every sheet through the sheet adapter; sheet.tsx stays as Silo wrote it", () => {
    expect(previewPluginSource).toContain('"components/ui/sheet.tsx": "adapters/sheet.tsx"');
    expect(read("./preview/adapters/sheet.tsx")).toContain('import "@/bloem/sheet-overlay.css";');
    expect(read("../components/ui/sheet.tsx")).toContain('data-slot="sheet-overlay"');
    expect(declarations(read("./sheet-overlay.css"), '[data-slot="sheet-overlay"]')).toContain(
      "background-color: rgb(0 0 0 / 0.6);",
    );
  });
});
