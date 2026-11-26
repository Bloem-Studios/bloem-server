import { FileCode2 } from "lucide-react";

import { bloemEnglishText, bloemT, type BloemMessageKey } from "@/bloem/i18n";
import type { UserSettingsNavGroup } from "@/lib/userSettingsNav";

import { SOURCE_CODE_SETTINGS_PATH } from "./api";

/** English search terms; they match in every language. */
const KEYWORDS = [
  "source",
  "code",
  "source code",
  "license",
  "licence",
  "agpl",
  "gpl",
  "gnu",
  "affero",
  "open source",
  "free software",
  "copyleft",
  "download",
  "version",
  "about",
] as const;

/** A message in the interface language, plus its English when that differs. */
function withEnglish(id: BloemMessageKey): { text: string; english: string[] } {
  const text = bloemT(id);
  const english = bloemEnglishText(id);
  return { text, english: english === text ? [] : [english] };
}

/**
 * The personal settings group Bloem adds last: About, holding the Source code
 * page. Every profile gets it, not only primary or admin ones, because the
 * license offers the source to everyone who uses the server.
 */
export function sourceCodeSettingsGroup(): UserSettingsNavGroup {
  const group = withEnglish("bloem.sourceCode.nav.group");
  const label = withEnglish("bloem.sourceCode.nav.label");
  const description = withEnglish("bloem.sourceCode.nav.description");
  const button = withEnglish("bloem.sourceCode.download.button");
  return {
    label: group.text,
    items: [
      {
        path: SOURCE_CODE_SETTINGS_PATH,
        label: label.text,
        icon: FileCode2,
        description: description.text,
        keywords: [...KEYWORDS, ...group.english, ...label.english, ...description.english],
        settings: [
          button.english.length > 0
            ? { label: button.text, keywords: button.english }
            : { label: button.text },
        ],
      },
    ],
  };
}
