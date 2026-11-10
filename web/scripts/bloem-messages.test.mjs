// @vitest-environment node
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { generate } from "./bloem-messages.mjs";

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CATALOG = path.join(WEB, "src/bloem/i18n/catalog");
const OUT = path.join(WEB, "src/bloem/locales");

async function catalog() {
  const texts = {};
  for (const name of await readdir(CATALOG)) {
    if (name.endsWith(".arb"))
      texts[name.slice(0, -4)] = await readFile(path.join(CATALOG, name), "utf8");
  }
  return texts;
}

const arb = (entries) => JSON.stringify({ "@@locale": "en", ...entries });

describe("bloem-messages", () => {
  it("has generated the committed message files from the catalog", async () => {
    const files = await generate(await catalog());
    for (const [name, contents] of Object.entries(files)) {
      expect(
        await readFile(path.join(OUT, name), "utf8"),
        `${name}: run node scripts/bloem-messages.mjs`,
      ).toBe(contents);
    }
    expect((await readdir(OUT)).sort()).toEqual(Object.keys(files).sort());
  });

  it.each([
    ["a message without a description", { en: arb({ "bloem.a.b": "Hi" }) }, /no description/],
    [
      "a key outside bloem.*",
      { en: arb({ "web.a": "Hi", "@web.a": { description: "x" } }) },
      /not bloem\./,
    ],
    [
      "a quoting apostrophe",
      { en: arb({ "bloem.a.b": "{name}'s", "@bloem.a.b": { description: "x" } }) },
      /apostrophe/,
    ],
    [
      "a translation with other placeholders",
      {
        en: arb({ "bloem.a.b": "Hi {name}", "@bloem.a.b": { description: "x" } }),
        de: JSON.stringify({ "bloem.a.b": "Hallo {nom}" }),
      },
      /uses \{nom\}, English uses \{name\}/,
    ],
    [
      "a translation of a message English lacks",
      {
        en: arb({ "bloem.a.b": "Hi", "@bloem.a.b": { description: "x" } }),
        de: JSON.stringify({ "bloem.a.c": "Hallo" }),
      },
      /not in en\.arb/,
    ],
  ])("refuses %s", async (_name, texts, error) => {
    await expect(generate(texts)).rejects.toThrow(error);
  });
});
