import { useOnViewTranslation as useOriginal } from "../../../hooks/useOnViewTranslation";
import { usePreviewMode } from "../usePreviewMode";
export function useOnViewTranslation(item: Parameters<typeof useOriginal>[0]) {
  const { active } = usePreviewMode();
  const original = useOriginal(active ? undefined : item);
  return active ? { translating: false, onTranslate: undefined } : original;
}
