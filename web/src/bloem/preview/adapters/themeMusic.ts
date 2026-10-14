import { useThemeMusic as useOriginal } from "../../../pages/ItemDetail/useThemeMusic";
import { usePreviewMode } from "../usePreviewMode";
export function useThemeMusic(item: Parameters<typeof useOriginal>[0], loading: boolean) {
  useOriginal(usePreviewMode().active ? undefined : item, loading);
}
