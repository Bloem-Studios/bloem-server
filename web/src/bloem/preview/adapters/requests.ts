import { useRequestFeatureStatus as useOriginal } from "../../../hooks/queries/useRequests";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../hooks/queries/useRequests";
export function useRequestFeatureStatus(options: Parameters<typeof useOriginal>[0] = {}) {
  return useOriginal({
    ...options,
    enabled: !usePreviewMode().active && (options.enabled ?? true),
  });
}
