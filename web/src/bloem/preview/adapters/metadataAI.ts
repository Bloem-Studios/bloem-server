import { useMetadataAIStatus as useOriginal } from "../../../hooks/queries/metadataAI";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../hooks/queries/metadataAI";
export function useMetadataAIStatus(enabled = true) {
  const { active } = usePreviewMode();
  return useOriginal(enabled && !active);
}
