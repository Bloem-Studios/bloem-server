import { useCallback, type ReactNode } from "react";

import { bloemT, useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import {
  t as siloT,
  useT as useSiloT,
  type MessageKey,
  type RichValues,
  type TextValues,
  type Translate,
} from "@/i18n";

export * from "@/i18n";

function isBloemKey(id: string): id is BloemMessageKey {
  return id.startsWith("bloem.");
}

/**
 * Silo's `useT` for a Silo module that formats ids from a list Bloem adds to
 * (an account card's changed rows): `bloem.*` ids come from Bloem's catalog.
 */
export function useT(): Translate {
  const silo = useSiloT();
  const bloem = useBloemT();
  return useCallback(
    (id: MessageKey | BloemMessageKey, values?: RichValues): ReactNode =>
      isBloemKey(id) ? bloem(id, values ?? {}) : silo(id, values ?? {}),
    [silo, bloem],
  ) as Translate;
}

/** Silo's `t`, with `bloem.*` ids from Bloem's catalog. */
export function t(id: MessageKey | BloemMessageKey, values?: TextValues): string {
  return isBloemKey(id) ? bloemT(id, values) : siloT(id, values);
}
