import { useEffectiveSettings as useOriginal } from "../../../hooks/queries/settingValues";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../hooks/queries/settingValues";
export function useEffectiveSettings(options?: Parameters<typeof useOriginal>[0]) {
  const { active } = usePreviewMode();
  return useOriginal({ ...options, enabled: !active && (options?.enabled ?? true) });
}

import {
  useSettingValue as useOriginalValue,
  useSettingsCapabilities as useOriginalCapabilities,
} from "../../../hooks/queries/settingValues";
import { SETTING_DEFINITIONS, type SettingKey } from "@/lib/settingsContract";
export function useSettingValue<T = unknown>(
  key: SettingKey,
  options?: Parameters<typeof useOriginalValue>[1],
) {
  const { active } = usePreviewMode();
  const result = useOriginalValue<T>(key, {
    ...options,
    enabled: !active && (options?.enabled ?? true),
  });
  return active
    ? {
        ...result,
        isPending: false,
        isLoading: false,
        setting: undefined,
        constrained: false,
        source: "default" as const,
        value: SETTING_DEFINITIONS[key].defaultValue as T,
      }
    : result;
}
export function useSettingsCapabilities(options?: Parameters<typeof useOriginalCapabilities>[0]) {
  const { active } = usePreviewMode();
  const result = useOriginalCapabilities({
    ...options,
    enabled: !active && (options?.enabled ?? true),
  });
  return active ? { ...result, data: undefined, isLoading: false } : result;
}
