import { describe, expect, it } from "vitest";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { applyBrand, applyCatalogBrand } from "./product-brand";

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

  // Rendered identity is covered by SiloBrand.bloem.test.tsx and the Vite
  // adapter's dev/build tests; upstream source intentionally keeps its identity.
  it("ships owned public images without restricted upstream artwork", () => {
    const publicPath = (name: string) => resolve(process.cwd(), "public", name);
    for (const restricted of ["silo-icon-1024.png", "silo-wordmark-sidebar.png"]) {
      expect(existsSync(publicPath(restricted))).toBe(false);
    }
    for (const owned of ["bloem-icon-1024.png", "bloem-wordmark-sidebar.png"]) {
      expect(existsSync(publicPath(owned))).toBe(true);
    }
  });
});

// Silo's apps on a Bloem server are woken through Silo's relay
// (docs/architecture/bloem-push-relays.md). Silo's relay card, its settings
// search entry, the setup step and the viewer's privacy notice describe that
// relay, so they keep Silo's name; the server itself is still this build.
describe("Silo's push relay keeps Silo's name", () => {
  it("leaves the relay card's title alone", () => {
    expect(applyBrand('const PUSH_RELAY_TITLE = "Silo Push Relay";')).toBe(
      'const PUSH_RELAY_TITLE = "Silo Push Relay";',
    );
    expect(applyBrand("Silo Server")).toBe("Bloem Server");
  });

  it("leaves Silo's relay messages alone and brands the rest", () => {
    const relay = [
      { type: 0, value: "Mobile push through Silo's relay, delivered by APNs or FCM." },
    ];
    const other = [{ type: 0, value: "Silo couldn't reach the server" }];
    expect(
      applyCatalogBrand({
        "web.admin.settings.notifications.relay.description": relay,
        "web.admin.settings.search.notifications.setting.siloPushRelay": [
          { type: 0, value: "Relais push Silo" },
        ],
        "web.setup.features.pushDescription": [
          {
            type: 0,
            value: "Delivers to the iOS and Android apps through Silo's open-source relay.",
          },
        ],
        "web.other": other,
      }),
    ).toEqual({
      "web.admin.settings.notifications.relay.description": relay,
      "web.admin.settings.search.notifications.setting.siloPushRelay": [
        { type: 0, value: "Relais push Silo" },
      ],
      "web.setup.features.pushDescription": [
        {
          type: 0,
          value: "Delivers to the iOS and Android apps through Silo's open-source relay.",
        },
      ],
      "web.other": [{ type: 0, value: "Bloem couldn't reach the server" }],
    });
  });

  it("names this server, then Silo's relay, in the privacy notice", () => {
    const notice = applyCatalogBrand({
      "web.notifications.pushPrivacy.relay": [
        {
          type: 0,
          value:
            "When push notifications are enabled, your Silo Server sends a content-free request to Silo's push relay so Silo can deliver notifications. The relay is ",
        },
        { type: 8, value: "link", children: [{ type: 0, value: "fully open source" }] },
      ],
    });
    expect(notice["web.notifications.pushPrivacy.relay"]).toEqual([
      {
        type: 0,
        value:
          "When push notifications are enabled, your Bloem Server sends a content-free request to Silo's push relay so Silo can deliver notifications. The relay is ",
      },
      { type: 8, value: "link", children: [{ type: 0, value: "fully open source" }] },
    ]);
  });

  it("holds in every language: the notice names the server before Silo's relay", async () => {
    const { readdirSync, readFileSync } = await import("node:fs");
    const dir = resolve(process.cwd(), "src/locales");
    const languages = readdirSync(dir).filter((file) => file.endsWith(".json"));
    expect(languages.length).toBeGreaterThanOrEqual(39);
    for (const file of languages) {
      const messages = JSON.parse(readFileSync(resolve(dir, file), "utf8")) as Record<
        string,
        unknown
      >;
      const before = JSON.stringify(messages["web.notifications.pushPrivacy.relay"]);
      const after = JSON.stringify(
        applyCatalogBrand({
          "web.notifications.pushPrivacy.relay": messages["web.notifications.pushPrivacy.relay"],
        })["web.notifications.pushPrivacy.relay"],
      );
      const silo = (text: string) => (text.match(/Silo/g) ?? []).length;
      // The en-XA pseudo-locale spells the name with accents; nothing brands it.
      if (file === "en-XA.json") continue;
      // One "Silo" becomes this build; at least one still names Silo's relay.
      expect(silo(before) - silo(after), file).toBe(1);
      expect(silo(after), file).toBeGreaterThanOrEqual(1);
    }
  });
});
