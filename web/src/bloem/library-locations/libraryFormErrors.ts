import { useMemo } from "react";

import { bloemT } from "@/bloem/i18n";

import type { LibraryFormErrors } from "@/components/admin/libraries/useLibraryForm";
import type { LibraryLocationsController } from "./useLibraryLocations";

interface LibraryLocationInput {
  /** The library being edited, or null while creating one. */
  library: object | null;
  paths: string[];
  storageSource: string;
  usesStorage: boolean;
  locations: LibraryLocationsController;
}

/**
 * Silo's library form errors with Bloem's library locations applied. Silo
 * asks every library for a folder; a library whose folders come from
 * administrative-context locations, or that reads a storage source, needs
 * those instead.
 */
export function bloemLibraryErrors(
  silo: LibraryFormErrors,
  { library, paths, storageSource, usesStorage, locations }: LibraryLocationInput,
): LibraryFormErrors {
  const { paths: missingFolder, ...rest } = silo;
  const next: LibraryFormErrors = { ...rest };
  if (locations.enabled) {
    const keepsLocations = Boolean(library) && Boolean(locations.original?.locations.length);
    if (missingFolder && !keepsLocations && !locations.selections.length) {
      next.paths = missingFolder;
    }
    const locationError = locations.shapeError(paths);
    if (locationError) next.paths = locationError;
  } else if (usesStorage) {
    if (!library && !storageSource) {
      next.storageSource = bloemT("bloem.admin.libraries.form.chooseStorageSource");
    }
  } else if (missingFolder) {
    next.paths = missingFolder;
  }
  return next;
}

export function useBloemLibraryErrors(
  silo: LibraryFormErrors,
  input: LibraryLocationInput,
): LibraryFormErrors {
  const { library, paths, storageSource, usesStorage, locations } = input;
  return useMemo(
    () => bloemLibraryErrors(silo, { library, paths, storageSource, usesStorage, locations }),
    [silo, library, paths, storageSource, usesStorage, locations],
  );
}

/**
 * The form's `errors` field, spread over Silo's: Bloem's errors, shown once a
 * save was attempted. (Optional in its type so the spread may replace Silo's.)
 */
export function bloemShownErrors(
  submitAttempted: boolean,
  errors: LibraryFormErrors,
): { errors?: LibraryFormErrors } {
  return { errors: submitAttempted ? errors : {} };
}
