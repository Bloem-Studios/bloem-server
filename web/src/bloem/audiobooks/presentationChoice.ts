// The listener's remembered presentation per server, profile and item. It is
// the only reliable record of which recording someone is listening to: the
// server never learns it (multipart sessions persist progress client-side).
import { useCallback, useMemo, useState } from "react";
import { useCurrentProfile } from "@/hooks/useCurrentProfile";

const KEY_PREFIX = "bloem-audiobook-presentation:v1";

export interface PresentationChoiceScope {
  server: string;
  profileId: string;
  contentId: string;
}

export function presentationChoiceKey({ server, profileId, contentId }: PresentationChoiceScope) {
  return [KEY_PREFIX, server, profileId, contentId].map(encodeURIComponent).join(":");
}

export function readPresentationChoice(scope: PresentationChoiceScope): string | null {
  try {
    return localStorage.getItem(presentationChoiceKey(scope));
  } catch {
    return null;
  }
}

export function writePresentationChoice(scope: PresentationChoiceScope, presentationId: string) {
  try {
    localStorage.setItem(presentationChoiceKey(scope), presentationId);
  } catch {
    // Storage full or unavailable: the choice lasts for this page only.
  }
}

/** The remembered presentation of `contentId` for the current server and profile. */
export function useRememberedPresentation(
  contentId: string | undefined,
): [string | null, (presentationId: string) => void] {
  const { profile } = useCurrentProfile();
  const profileId = profile?.id ?? "";
  const server = typeof window === "undefined" ? "" : window.location.origin;
  // Keep the page choice even when browser storage rejects a write.
  const [pageChoice, setPageChoice] = useState<{ key: string; id: string } | null>(null);
  const scope = useMemo<PresentationChoiceScope | null>(
    () => (contentId && profileId ? { server, profileId, contentId } : null),
    [contentId, profileId, server],
  );
  const remembered = useMemo(() => {
    if (!scope) return null;
    return pageChoice?.key === presentationChoiceKey(scope)
      ? pageChoice.id
      : readPresentationChoice(scope);
  }, [scope, pageChoice]);
  const remember = useCallback(
    (presentationId: string) => {
      if (!scope) return;
      writePresentationChoice(scope, presentationId);
      setPageChoice({ key: presentationChoiceKey(scope), id: presentationId });
    },
    [scope],
  );
  return [remembered, remember];
}
