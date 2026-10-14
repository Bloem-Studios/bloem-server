import { useState } from "react";
import {
  useSearchMediaScope as useOriginal,
  type SearchMediaScope,
  DEFAULT_SEARCH_MEDIA_SCOPE,
} from "../../../hooks/useSearchMediaScope";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../hooks/useSearchMediaScope";
export function useSearchMediaScope() {
  const original = useOriginal();
  const { active } = usePreviewMode();
  const [scope, setScope] = useState<SearchMediaScope>(DEFAULT_SEARCH_MEDIA_SCOPE);
  return active ? { scope, setScope } : original;
}
