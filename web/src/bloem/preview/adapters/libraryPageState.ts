import { useLibraryPageStatePreference as useOriginal } from "../../../hooks/queries/libraryPageState";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../hooks/queries/libraryPageState";
export function useLibraryPageStatePreference() {
  const result = useOriginal();
  const { active } = usePreviewMode();
  return active
    ? {
        ...result,
        ownerKey: null,
        isLoading: false,
        rememberEnabled: false,
        preference: { version: 1 as const, libraries: {} },
        saveLibrarySearch: () => Promise.resolve(),
      }
    : result;
}
