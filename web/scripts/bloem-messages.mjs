// Generates Bloem's web message files from its catalog, the way Silo's
// scripts/sync-translations installs Silo's: precompiled messages per
// language, a key type, and the two pseudo-locales.
//
//   src/bloem/i18n/catalog/<tag>.arb   the source: en.arb with a description
//                                      for every message, one file per language
//   src/bloem/locales/<tag>.json       messages outside bloem.admin.*
//   src/bloem/locales/<tag>.admin.json bloem.admin.* (loaded with the admin pages)
//   src/bloem/locales/keys.ts          BloemMessageKey and BLOEM_LOCALES
//
// Usage, from web/: node scripts/bloem-messages.mjs [--check]
// --check writes nothing and fails when a generated file is out of date.
import { readdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import * as prettier from "prettier";

// Loaded by file path: vite.config.ts aliases the package name to its
// no-parser build for the bundle, and Vitest applies that alias to tests.
const { parse, TYPE } = await import(
  pathToFileURL(createRequire(import.meta.url).resolve("@formatjs/icu-messageformat-parser")).href
);

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CATALOG = path.join(WEB, "src/bloem/i18n/catalog");
const OUT = path.join(WEB, "src/bloem/locales");
export const ADMIN_PREFIX = "bloem.admin.";
const KEY = /^bloem(\.[a-z][A-Za-z0-9]*)+$/;
// An ASCII apostrophe before a brace, # or | starts ICU quoting: the catalog
// rejects it. Use ’ in text, or double it ('') for a literal apostrophe.
const QUOTING_APOSTROPHE = /'[{}#|]/;

/** Reads an ARB file: messages by key, and English descriptions by key. */
function readArb(text, file) {
  const arb = JSON.parse(text);
  const messages = {};
  const descriptions = {};
  for (const [key, value] of Object.entries(arb)) {
    if (key.startsWith("@@")) continue;
    if (key.startsWith("@")) {
      descriptions[key.slice(1)] = value?.description;
      continue;
    }
    if (typeof value !== "string") throw new Error(`${file}: ${key} is not a string`);
    messages[key] = value;
  }
  return { messages, descriptions };
}

/** The argument names a message prints or chooses on, sorted. */
function argumentNames(ast, names = new Set()) {
  for (const element of ast) {
    if ("value" in element && element.type !== TYPE.literal) names.add(element.value);
    if (element.type === TYPE.select || element.type === TYPE.plural) {
      for (const option of Object.values(element.options)) argumentNames(option.value, names);
    }
    if (element.type === TYPE.tag) argumentNames(element.children, names);
  }
  return [...names].filter((name) => typeof name === "string").sort();
}

function compile(message, key, file) {
  if (QUOTING_APOSTROPHE.test(message)) {
    throw new Error(`${file}: ${key} has an ASCII apostrophe before a brace, # or |; use ’`);
  }
  try {
    return parse(message);
  } catch (error) {
    throw new Error(`${file}: ${key} does not parse: ${error.message}`, { cause: error });
  }
}

// --- Pseudo-locales, shaped like Silo's ------------------------------------

const ACCENTED = Object.fromEntries(
  [..."abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"].map((letter, index) => [
    letter,
    [..."åƀçđéƒĝĥîĵķļɱñöþǫŕšţûṽŵẋýžÅƁÇĐÉƑĜĤÎĴĶĻṀÑÖÞǪŔŠŢÛṼŴẊÝŽ"][index],
  ]),
);

function literalLength(ast) {
  return ast.reduce(
    (sum, element) => sum + (element.type === TYPE.literal ? element.value.length : 0),
    0,
  );
}

/** en-XA: accented, about a third longer, and bracketed so clipping shows. */
function accented(ast) {
  const out = ast.map((element) => {
    if (element.type === TYPE.literal) {
      return { ...element, value: element.value.replace(/[A-Za-z]/g, (c) => ACCENTED[c]) };
    }
    if (element.type === TYPE.select || element.type === TYPE.plural) {
      const options = Object.fromEntries(
        Object.entries(element.options).map(([name, option]) => [
          name,
          { ...option, value: accented(option.value) },
        ]),
      );
      return { ...element, options };
    }
    if (element.type === TYPE.tag) return { ...element, children: accented(element.children) };
    return element;
  });
  const padding = "~".repeat(Math.floor(literalLength(ast) / 2));
  if (out[0]?.type === TYPE.literal) out[0] = { ...out[0], value: `⟦${out[0].value}` };
  else out.unshift({ type: TYPE.literal, value: "⟦" });
  const last = out.at(-1);
  if (last?.type === TYPE.literal)
    out[out.length - 1] = { ...last, value: `${last.value}${padding}⟧` };
  else out.push({ type: TYPE.literal, value: `${padding}⟧` });
  return out;
}

const RLM = "‏";
const RLO = "‮";
const PDF = "‬";

/** ar-XB: English text forced right to left, so mirroring bugs show. */
function mirrored(ast) {
  const out = ast.map((element) => {
    if (element.type === TYPE.literal) return { ...element, value: `${RLO}${element.value}${PDF}` };
    if (element.type === TYPE.select || element.type === TYPE.plural) {
      const options = Object.fromEntries(
        Object.entries(element.options).map(([name, option]) => [
          name,
          { ...option, value: mirrored(option.value) },
        ]),
      );
      return { ...element, options };
    }
    if (element.type === TYPE.tag) return { ...element, children: mirrored(element.children) };
    return element;
  });
  if (out[0]?.type === TYPE.literal) out[0] = { ...out[0], value: `${RLM}${out[0].value}` };
  else out.unshift({ type: TYPE.literal, value: RLM });
  return out;
}

// --- Generation --------------------------------------------------------------

/**
 * Builds every generated file from the catalog's ARB texts (tag → text).
 * Returns output paths relative to src/bloem/locales → contents.
 */
export async function generate(arbTexts) {
  if (!arbTexts.en) throw new Error("the catalog has no en.arb");
  const english = readArb(arbTexts.en, "en.arb");
  const keys = Object.keys(english.messages).sort();
  const englishAst = {};
  for (const key of keys) {
    if (!KEY.test(key)) throw new Error(`en.arb: ${key} is not bloem.<lowerCamel segments>`);
    if (!english.descriptions[key]?.trim()) {
      throw new Error(`en.arb: ${key} has no description saying where it appears`);
    }
    englishAst[key] = compile(english.messages[key], key, "en.arb");
  }
  for (const key of Object.keys(english.descriptions)) {
    if (!(key in english.messages)) throw new Error(`en.arb: @${key} describes no message`);
  }

  const languages = { en: englishAst };
  for (const tag of Object.keys(arbTexts).sort()) {
    if (tag === "en") continue;
    const file = `${tag}.arb`;
    const { messages } = readArb(arbTexts[tag], file);
    const compiled = {};
    for (const key of Object.keys(messages).sort()) {
      if (!(key in englishAst)) throw new Error(`${file}: ${key} is not in en.arb`);
      const ast = compile(messages[key], key, file);
      const want = argumentNames(englishAst[key]).join(",");
      const got = argumentNames(ast).join(",");
      if (want !== got) throw new Error(`${file}: ${key} uses {${got}}, English uses {${want}}`);
      compiled[key] = ast;
    }
    languages[tag] = compiled;
  }
  languages["en-XA"] = Object.fromEntries(keys.map((key) => [key, accented(englishAst[key])]));
  languages["ar-XB"] = Object.fromEntries(keys.map((key) => [key, mirrored(englishAst[key])]));

  const files = {};
  for (const [tag, messages] of Object.entries(languages)) {
    const app = {};
    const admin = {};
    for (const key of Object.keys(messages).sort()) {
      (key.startsWith(ADMIN_PREFIX) ? admin : app)[key] = messages[key];
    }
    files[`${tag}.json`] = `${JSON.stringify(app, null, 2)}\n`;
    files[`${tag}.admin.json`] = `${JSON.stringify(admin, null, 2)}\n`;
  }
  const shipped = [
    "en",
    ...Object.keys(arbTexts)
      .filter((tag) => tag !== "en")
      .sort(),
  ];
  const keysTs = [
    "// Generated by scripts/bloem-messages.mjs from src/bloem/i18n/catalog/<tag>.arb and <tag>/*.arb; do not edit.",
    "",
    `export type BloemMessageKey = ${keys.length ? keys.map((key) => JSON.stringify(key)).join(" | ") : "never"};`,
    "",
    "/** Languages with a Bloem catalog; the pseudo-locales are generated from English. */",
    `export const BLOEM_LOCALES = ${JSON.stringify(shipped)} as const;`,
    "",
  ].join("\n");
  const keysPath = path.join(OUT, "keys.ts");
  const options = { ...(await prettier.resolveConfig(keysPath)), filepath: keysPath, plugins: [] };
  files["keys.ts"] = await prettier.format(keysTs, options);
  return files;
}

/**
 * The catalog's ARB texts by language: `<tag>.arb`, and the files in `<tag>/`
 * (one per area, so areas are edited apart), merged. A key in two files fails.
 */
export async function readCatalog(dir = CATALOG) {
  const parts = {};
  const add = async (tag, file) => {
    const text = await readFile(file, "utf8");
    (parts[tag] ??= []).push([path.relative(dir, file), JSON.parse(text)]);
  };
  for (const entry of (await readdir(dir, { withFileTypes: true })).sort((x, y) =>
    x.name < y.name ? -1 : 1,
  )) {
    if (entry.isFile() && entry.name.endsWith(".arb"))
      await add(entry.name.slice(0, -4), path.join(dir, entry.name));
    if (entry.isDirectory()) {
      for (const name of (await readdir(path.join(dir, entry.name))).sort()) {
        if (name.endsWith(".arb")) await add(entry.name, path.join(dir, entry.name, name));
      }
    }
  }
  const texts = {};
  for (const [tag, files] of Object.entries(parts)) {
    const merged = { "@@locale": tag };
    const from = {};
    for (const [file, arb] of files) {
      for (const [key, value] of Object.entries(arb)) {
        if (key === "@@locale") continue;
        if (key in merged) throw new Error(`${file}: ${key} is also in ${from[key]}`);
        merged[key] = value;
        from[key] = file;
      }
    }
    texts[tag] = JSON.stringify(merged);
  }
  return texts;
}

async function main() {
  const check = process.argv.includes("--check");
  const files = await generate(await readCatalog());
  const present = new Set(
    (await readdir(OUT).catch(() => [])).filter((name) => name !== ".gitkeep"),
  );
  const stale = [];
  for (const [name, contents] of Object.entries(files)) {
    present.delete(name);
    const current = await readFile(path.join(OUT, name), "utf8").catch(() => null);
    if (current === contents) continue;
    stale.push(name);
    if (!check) await writeFile(path.join(OUT, name), contents);
  }
  for (const name of present) stale.push(`${name} (no longer generated; delete it)`);
  if (check && stale.length) {
    console.error(
      `bloem-messages: out of date, run node scripts/bloem-messages.mjs: ${stale.join(", ")}`,
    );
    process.exit(1);
  }
  if (!check)
    console.error(`bloem-messages: ${stale.length ? `wrote ${stale.join(", ")}` : "up to date"}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await main();
