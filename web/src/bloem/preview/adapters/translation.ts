import { useOnViewTranslation as useOriginal } from "../../../hooks/useOnViewTranslation";
import { usePreviewMode } from "../usePreviewMode";
// Forwards every argument: Silo's options (the hero's poll callback and
// pending slides) must reach the hook outside preview mode.
export function useOnViewTranslation(...[item, options]: Parameters<typeof useOriginal>) {
  const { active } = usePreviewMode();
  const original = useOriginal(active ? undefined : item, options);
  return active ? { translating: false, onTranslate: undefined } : original;
}
