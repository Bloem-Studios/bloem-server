import { useCallback, useEffect, useSyncExternalStore, type ReactNode } from "react";

import {
  appIntl,
  createAppIntl,
  numberText,
  subscribeAppIntl,
  useAppLanguage,
  type Messages,
  type RichValues,
  type TextValues,
} from "@/i18n";
import type { BloemMessageKey } from "@/bloem/locales/keys";
import { interfaceDirection } from "@/lib/appLocale";
import {
  BLOEM_ADMIN_PREFIX,
  bloemAdminLoadFailure,
  bloemAdminMessagesReady,
  bloemEnglishMessage,
  bloemMessagesVersion,
  bloemTranslations,
  catalogTag,
  requestBloemAdminMessages,
  setBloemLanguage,
  subscribeBloemMessages,
} from "./catalog";

type Intl = ReturnType<typeof createAppIntl>;
type Ast = NonNullable<Messages[string]>;
type Element = Ast[number];

/** Formats a Bloem message id; rich values (tags) return React nodes. */
export interface BloemTranslate {
  (id: BloemMessageKey, values?: TextValues): string;
  (id: BloemMessageKey, values: RichValues): ReactNode;
}

// --- Values, as Silo's src/i18n/format.ts prepares them ------------------------
//
// In a right-to-left interface every printed string value is wrapped in
// Unicode isolates (bare numbers are not); in a language's own translation
// numbers print in its digits. Selector values are never changed. The test
// beside this file checks the output against Silo's formatter.

const FSI = "⁨";
const PDI = "⁩";
const ARGUMENT = 1;
const SELECT = 5;
const PLURAL = 6;
const TAG = 8;
const DIGITS = /^\p{Nd}+$/u;

const uses = new WeakMap<Ast, { printed: Set<string>; selectors: Set<string> }>();

function argumentUse(ast: Ast) {
  let use = uses.get(ast);
  if (use) return use;
  const found = { printed: new Set<string>(), selectors: new Set<string>() };
  const walk = (elements: Element[]) => {
    for (const element of elements) {
      if (element.type === ARGUMENT) found.printed.add(element.value);
      else if (element.type === SELECT || element.type === PLURAL) {
        found.selectors.add(element.value);
        for (const option of Object.values(element.options)) walk(option.value);
      } else if (element.type === TAG) walk(element.children);
    }
  };
  walk(ast);
  uses.set(ast, found);
  use = found;
  return use;
}

function prepareValues<V extends RichValues>(
  intl: Intl,
  ast: Ast,
  values: V | undefined,
  translated: boolean,
): V | undefined {
  if (!values) return values;
  const rtl = interfaceDirection(intl.locale) === "rtl";
  const localDigits = translated && numberText(0, intl) !== "0";
  if (!rtl && !localDigits) return values;
  const { printed, selectors } = argumentUse(ast);
  const prepared: RichValues = { ...values };
  for (const name of printed) {
    if (selectors.has(name)) continue;
    let value = values[name];
    if (typeof value === "number") value = localDigits ? numberText(value, intl) : String(value);
    if (rtl && typeof value === "string" && !DIGITS.test(value)) value = FSI + value + PDI;
    prepared[name] = value;
  }
  return prepared as V;
}

/**
 * Formats one message: the translation in `intl`'s messages if it has one,
 * else the English AST with English plural rules (Silo's fallback).
 */
export function formatMessageAst(
  intl: Intl,
  id: string,
  english: Ast,
  values?: RichValues,
): ReactNode {
  const own = intl.messages[id];
  const translated = Array.isArray(own) ? (own as Ast) : undefined;
  return intl.formatMessage(
    { id, defaultMessage: english },
    prepareValues(intl, translated ?? english, values, translated !== undefined) as TextValues,
  );
}

// --- Formatters per language ---------------------------------------------------

const formatters = new Map<string, { version: number; intl: Intl }>();

function formatterFor(locale: string, version: number): Intl {
  const cached = formatters.get(locale);
  if (cached?.version === version) return cached.intl;
  const intl = createAppIntl(locale, bloemTranslations(catalogTag(locale)));
  formatters.set(locale, { version, intl });
  return intl;
}

function formatBloem(locale: string, version: number, id: BloemMessageKey, values?: RichValues) {
  const english = bloemEnglishMessage(id);
  if (!english) {
    // Admin text before its part has loaded: ask for it; useBloemT re-renders.
    if (import.meta.env.MODE === "test") throw new Error(`bloemT: ${id} is not loaded`);
    if (id.startsWith(BLOEM_ADMIN_PREFIX)) {
      queueMicrotask(() => requestBloemAdminMessages(catalogTag(locale)));
    }
    return "";
  }
  return formatMessageAst(formatterFor(locale, version), id, english, values);
}

// Bloem follows Silo's interface language: each new formatter loads Bloem's messages too.
subscribeAppIntl(() => setBloemLanguage(appIntl().locale));
setBloemLanguage(appIntl().locale);

/**
 * Message formatting for a component; it re-renders when the language
 * changes and when more Bloem messages load.
 */
export function useBloemT(): BloemTranslate {
  const locale = useAppLanguage();
  const version = useSyncExternalStore(
    subscribeBloemMessages,
    bloemMessagesVersion,
    bloemMessagesVersion,
  );
  useEffect(() => setBloemLanguage(locale), [locale]);
  return useCallback(
    (id: BloemMessageKey, values?: RichValues) => formatBloem(locale, version, id, values),
    [locale, version],
  ) as BloemTranslate;
}

/**
 * Asks for Bloem's admin text and reports whether it is ready in the current
 * language. Bloem's admin outlet renders nothing until it is, as Silo's admin
 * layout waits for Silo's.
 */
export function useBloemAdminMessages(): boolean {
  const locale = useAppLanguage();
  useEffect(() => requestBloemAdminMessages(catalogTag(locale)), [locale]);
  const ready = useSyncExternalStore(
    subscribeBloemMessages,
    bloemAdminMessagesReady,
    bloemAdminMessagesReady,
  );
  const failure = bloemAdminLoadFailure();
  if (!ready && failure !== undefined) throw failure;
  return ready;
}

/** A Bloem message in English whatever the language: search terms and other data. */
export function bloemEnglishText(id: BloemMessageKey, values?: TextValues): string {
  return formatBloem("en", bloemMessagesVersion(), id, values) as string;
}

/**
 * Formats a Bloem message in the current language, for code outside
 * components. Call it when the text is used, never at module scope.
 */
export function bloemT(id: BloemMessageKey, values?: TextValues): string {
  return formatBloem(appIntl().locale, bloemMessagesVersion(), id, values) as string;
}
