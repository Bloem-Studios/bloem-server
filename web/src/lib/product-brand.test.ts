import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { applyBrand } from "./product-brand";

describe("applyBrand", () => {
  // The whole point: upstream's prose, rebranded, without editing upstream's
  // source. These are real strings from the tree.
  it("rebrands the product name in prose", () => {
    expect(
      applyBrand('"Pause could not reach the player directly. Silo will end the session."'),
    ).toBe('"Pause could not reach the player directly. Bloem will end the session."');
    expect(applyBrand("Managed by Silo")).toBe("Managed by Bloem");
    expect(applyBrand("Silo will download it server-side before importing.")).toBe(
      "Bloem will download it server-side before importing.",
    );
  });

  /*
   * Everything below is load-bearing. A transform that rewrote any of these
   * would break the build or the wire, and would do it silently — the bundle
   * still compiles, the field name just stops matching the API.
   */
  it("leaves identifiers, fields, paths and URLs alone", () => {
    const untouched = [
      'import { SiloBrand } from "@/components/SiloBrand";',
      "type SiloThemeFile = { name: string };",
      "const id = payload.silo_profile_id;",
      'scope: "silo_custom.scope"',
      'plugin: "silo.autoscan.arr"',
      'src="/silo-wordmark-sidebar.png"',
      '"https://github.com/Silo-Server/example-plugin"',
      "module github.com/Silo-Server/silo-server",
      '"/tmp/silo-transcode/test-extras"',
      "const siloUser = users[0];",
    ];
    for (const line of untouched) {
      expect(applyBrand(line), line).toBe(line);
    }
  });

  // A brand at the very start of a string has nothing before it to anchor on,
  // which is the case a naive lookbehind-free pattern drops.
  it("rebrands at the start of a line", () => {
    expect(applyBrand("Silo is running.")).toBe("Bloem is running.");
  });

  // Possessives and punctuation are still prose.
  it("rebrands before punctuation", () => {
    expect(applyBrand("filled from Silo's config")).toBe("filled from Bloem's config");
    expect(applyBrand("powered by Silo.")).toBe("powered by Bloem.");
    expect(applyBrand("(Silo)")).toBe("(Bloem)");
  });

  // Two in one string, which a non-global pattern would half-do.
  it("rebrands every occurrence", () => {
    expect(applyBrand("Silo starts, then Silo stops.")).toBe("Bloem starts, then Bloem stops.");
  });

  it("takes the name it is given", () => {
    expect(applyBrand("Silo is running.", "Meridian")).toBe("Meridian is running.");
  });

  it("ships Bloem public identity without restricted Silo brand assets", () => {
    const publicPath = (name: string) => resolve(process.cwd(), "public", name);
    const manifest = JSON.parse(readFileSync(publicPath("site.webmanifest"), "utf8"));
    expect(manifest.name).toBe("Bloem");
    expect(manifest.short_name).toBe("Bloem");

    const serviceWorker = readFileSync(publicPath("sw.js"), "utf8");
    expect(serviceWorker).toContain('data.title || "Bloem"');
    expect(serviceWorker).not.toContain('data.title || "Silo"');

    for (const restricted of ["silo-icon-1024.png", "silo-wordmark-sidebar.png"]) {
      expect(existsSync(publicPath(restricted))).toBe(false);
    }
    for (const owned of ["bloem-icon-1024.png", "bloem-wordmark-sidebar.png"]) {
      expect(existsSync(publicPath(owned))).toBe(true);
    }

    const brandComponent = readFileSync(
      resolve(process.cwd(), "src/components/SiloBrand.tsx"),
      "utf8",
    );
    expect(brandComponent).toContain('"/bloem-wordmark-sidebar.png"');
    expect(brandComponent).toContain('"/bloem-icon-1024.png"');
    expect(brandComponent).not.toContain('"/silo-wordmark-sidebar.png"');
    expect(brandComponent).not.toContain('"/silo-icon-1024.png"');
  });
});
