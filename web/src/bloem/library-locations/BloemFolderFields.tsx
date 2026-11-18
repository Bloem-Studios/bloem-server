import { Database } from "lucide-react";
import type { ReactNode } from "react";

import { useBloemT } from "@/bloem/i18n";
import type { LibraryFormController } from "@/components/admin/libraries/useLibraryForm";
import { Button } from "@/components/ui/button";
import { LibraryLocationsEditor } from "./LibraryLocationsEditor";

/** An existing library on a storage source: its location is fixed. */
function StorageSourceNotice() {
  const bt = useBloemT();
  return (
    <div className="flex items-center gap-1.5 text-sm">
      <Database className="text-muted-foreground/60 size-4 shrink-0" />
      <span>{bt("bloem.admin.libraries.form.storageSource")}</span>
      <span className="text-muted-foreground text-xs">
        {bt("bloem.admin.libraries.form.storageSourceFixed")}
      </span>
    </div>
  );
}

function StaleContextNotice() {
  const bt = useBloemT();
  return (
    <p role="alert" className="text-destructive text-sm">
      {bt("bloem.admin.libraries.form.contextChanged")}
    </p>
  );
}

function LocationsLoading({ form }: { form: LibraryFormController }) {
  const bt = useBloemT();
  return (
    <div className="space-y-2">
      <p role="status" className="text-muted-foreground text-sm">
        {form.locations.error || bt("bloem.admin.libraries.form.checkingLocations")}
      </p>
      <Button
        type="button"
        variant="outline"
        className="h-11 sm:h-9"
        onClick={() => void form.locations.reload()}
      >
        {bt("bloem.admin.libraries.form.reloadLocations")}
      </Button>
    </div>
  );
}

/**
 * What Silo's folder fields show instead of folders, when Bloem's library
 * locations decide where the library reads from, or the library already reads
 * a storage source; null when Silo's folder fields apply.
 */
export function bloemFolderFields(form: LibraryFormController): ReactNode | null {
  if (form.staleContext) return <StaleContextNotice />;
  if (form.locations.enabled) return <LibraryLocationsEditor form={form} />;
  if (form.locations.loading || form.locations.blocked) return <LocationsLoading form={form} />;
  if (form.usesStorage) return <StorageSourceNotice />;
  return null;
}
