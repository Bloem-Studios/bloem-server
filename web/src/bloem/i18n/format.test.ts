import { describe, expect, it } from "vitest";

import { createAppIntl, loadTranslations, type MessageKey, type TextValues } from "@/i18n";
import { englishMessage } from "@/i18n/catalog";
import { formatWith } from "@/i18n/format";

import { formatMessageAst } from "./format";

// Bloem's formatter repeats Silo's value handling (right-to-left isolates,
// local digits in a translation, English fallback); it must print what
// Silo's prints for the same message.
describe("formatMessageAst", () => {
  const MESSAGES: [MessageKey, TextValues][] = [
    ["web.notifications.inbox.episodeLine", { code: "S01E05", title: "Pilot" }],
    ["web.notifications.inbox.minutesAgo", { minutes: 42 }],
  ];

  it.each([
    ["en", undefined],
    ["ar", "ar-EG"],
    ["ar", "ar-AE"],
    ["ar-XB", undefined],
    ["de", undefined],
  ])("formats as Silo does in %s (format locale %s)", async (tag, formatLocale) => {
    const intl = createAppIntl(tag, await loadTranslations(tag), formatLocale);
    for (const [id, values] of MESSAGES) {
      expect(formatMessageAst(intl, id, englishMessage(id)!, values)).toBe(
        formatWith(intl, id, values),
      );
    }
  });

  it("falls back to English for a message the language lacks", async () => {
    const intl = createAppIntl("ar", {}, "ar-EG");
    const [id, values] = MESSAGES[1]!;
    expect(formatMessageAst(intl, id, englishMessage(id)!, values)).toBe(
      formatWith(intl, id, values),
    );
  });
});
