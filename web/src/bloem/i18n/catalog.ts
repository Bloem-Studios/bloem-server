import { adminMessagesRequested, subscribeAdminRequest, type Messages } from "@/i18n";

/**
 * Bloem's generated catalog (scripts/bloem-messages.mjs), split like Silo's:
 * English outside `bloem.admin.*` ships in the main bundle as every message's
 * fallback; the admin part and every other language are lazy chunks.
 */
export const BLOEM_ADMIN_PREFIX = "bloem.admin.";

// Silo's condition (src/i18n/catalog.ts), repeated so the static `false` drops
// the pseudo-locale globs from a production build.
const PSEUDO_LOCALES_ENABLED = import.meta.env.DEV || import.meta.env.VITE_PSEUDO_LOCALES === "1";

type Loaders = Record<string, () => Promise<Messages>>;
type Part = "app" | "admin";

const english = import.meta.glob<Messages>("../locales/en.json", {
  eager: true,
  import: "default",
})["../locales/en.json"]!;
const englishAdmin = import.meta.glob<Messages>("../locales/en.admin.json", {
  import: "default",
})["../locales/en.admin.json"]!;

const translations: Record<Part, Loaders> = {
  app: import.meta.glob<Messages>(
    [
      "../locales/*.json",
      "!../locales/*.admin.json",
      "!../locales/en.json",
      "!../locales/*-X?.json",
    ],
    { import: "default" },
  ),
  admin: import.meta.glob<Messages>(
    ["../locales/*.admin.json", "!../locales/en.admin.json", "!../locales/*-X?.admin.json"],
    { import: "default" },
  ),
};
const pseudoTranslations: Record<Part, Loaders> = PSEUDO_LOCALES_ENABLED
  ? {
      app: import.meta.glob<Messages>("../locales/*-X?.json", { import: "default" }),
      admin: import.meta.glob<Messages>("../locales/*-X?.admin.json", { import: "default" }),
    }
  : { app: {}, admin: {} };

// --- The store components follow ---------------------------------------------

let adminEnglish: Messages | undefined;
let adminEnglishError: unknown;
let adminWanted = false;
const loaded = new Map<string, Messages>();
const loading = new Map<string, Promise<void>>();
let version = 0;
const listeners = new Set<() => void>();

function changed(): void {
  version += 1;
  for (const listener of listeners) listener();
}

/** Calls `listener` whenever more Bloem messages have loaded. */
export function subscribeBloemMessages(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Changes whenever more Bloem messages have loaded (a store snapshot). */
export function bloemMessagesVersion(): number {
  return version;
}

/** The English message for an id; an admin message is undefined until its part loads. */
export function bloemEnglishMessage(id: string): Messages[string] {
  return english[id] ?? adminEnglish?.[id];
}

/** A language's loaded translations, both parts. */
export function bloemTranslations(tag: string): Messages {
  return { ...loaded.get(`${tag}:app`), ...loaded.get(`${tag}:admin`) };
}

function loadPart(tag: string, part: Part): Promise<void> {
  const key = `${tag}:${part}`;
  if (tag === "en" || loaded.has(key)) return Promise.resolve();
  const pending = loading.get(key);
  if (pending) return pending;
  const file = part === "admin" ? `../locales/${tag}.admin.json` : `../locales/${tag}.json`;
  const load = translations[part][file] ?? pseudoTranslations[part][file];
  if (!load) {
    loaded.set(key, {});
    return Promise.resolve();
  }
  const promise = load().then(
    (messages) => {
      loaded.set(key, messages);
      loading.delete(key);
      changed();
    },
    () => {
      // Without its own part (a chunk a newer release replaced), a language
      // shows Bloem's text in English, as Silo's does.
      loaded.set(key, {});
      loading.delete(key);
      changed();
    },
  );
  loading.set(key, promise);
  return promise;
}

function loadAdminEnglish(): Promise<void> {
  if (adminEnglish) return Promise.resolve();
  return englishAdmin().then(
    (messages) => {
      adminEnglish = messages;
      changed();
    },
    (error: unknown) => {
      adminEnglishError = error;
      changed();
    },
  );
}

/** Loads a language's Bloem messages: its app part, and its admin part once admin text is wanted. */
export function loadBloemLanguage(tag: string): void {
  void loadPart(tag, "app");
  if (adminWanted) void loadPart(tag, "admin");
}

/** Asks for Bloem's admin text, in English and in `tag`. Silo asking for its own asks too. */
export function requestBloemAdminMessages(tag: string): void {
  adminWanted = true;
  void loadAdminEnglish();
  void loadPart(tag, "admin");
}

/** The tag a formatter's locale names (`ar-u-nu-arab` is Arabic's catalog, `ar`). */
export function catalogTag(locale: string): string {
  try {
    return new Intl.Locale(locale).baseName;
  } catch {
    return locale;
  }
}

let currentTag = "en";

/** Follows Silo's interface language: `setBloemLanguage` is called with each formatter's locale. */
export function setBloemLanguage(locale: string): void {
  const tag = catalogTag(locale);
  if (tag === currentTag) return;
  currentTag = tag;
  loadBloemLanguage(tag);
  changed();
}

/** Whether Bloem's admin text renders in the current language (its parts have loaded). */
export function bloemAdminMessagesReady(): boolean {
  return adminEnglish !== undefined && (currentTag === "en" || loaded.has(`${currentTag}:admin`));
}

/** Why Bloem's English admin text failed to load, if it did. */
export function bloemAdminLoadFailure(): unknown {
  return adminEnglishError;
}

subscribeAdminRequest(() => {
  if (adminMessagesRequested() && !adminWanted) requestBloemAdminMessages(currentTag);
});
if (adminMessagesRequested()) requestBloemAdminMessages(currentTag);

/** Loads the English admin part, for tests that render Bloem's admin text. */
export async function loadBloemAdminEnglishForTests(): Promise<void> {
  adminWanted = true;
  await loadAdminEnglish();
}
