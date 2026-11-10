import { Database } from "lucide-react";
import type { ReactNode } from "react";

import { useBloemT } from "@/bloem/i18n";
import type { LibraryFormController } from "@/components/admin/libraries/useLibraryForm";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { storageSourceLabel } from "@/hooks/queries/admin/storageSources";
import { LibraryLocationsEditor } from "./LibraryLocationsEditor";

/**
 * Where an ebook library reads its books from: a storage source, chosen once
 * when the library is created, or filesystem folders.
 */
function StorageSourceFields({ form }: { form: LibraryFormController }) {
  const bt = useBloemT();
  if (form.library) {
    const source = form.storageSources.find((s) => s.source_key === form.storageSource);
    return (
      <div className="flex items-center gap-1.5 text-sm">
        <Database className="text-muted-foreground/60 size-4 shrink-0" />
        <span>
          {source ? storageSourceLabel(source) : bt("bloem.admin.libraries.form.storageSource")}
        </span>
        <span className="text-muted-foreground text-xs">
          {bt("bloem.admin.libraries.form.storageSourceFixed")}
        </span>
      </div>
    );
  }
  const usable = form.storageSources.filter((s) => s.enabled && s.configured);
  return (
    <div className="space-y-2">
      <Select value={form.storageSource} onValueChange={form.setStorageSource}>
        <SelectTrigger aria-invalid={form.errors.storageSource ? true : undefined}>
          <SelectValue
            placeholder={bt("bloem.admin.libraries.form.chooseStorageSourcePlaceholder")}
          />
        </SelectTrigger>
        <SelectContent>
          {usable.map((source) => (
            <SelectItem key={source.source_key} value={source.source_key}>
              {storageSourceLabel(source)}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {usable.length === 0 ? (
        <p className="text-muted-foreground text-xs">
          {bt("bloem.admin.libraries.form.noStorageSources")}
        </p>
      ) : null}
      {form.errors.storageSource ? (
        <p className="text-destructive text-xs">{form.errors.storageSource}</p>
      ) : null}
    </div>
  );
}

/** Folders or a storage source, for a new library of a type that can read one. */
export function LibraryLocationKindToggle({ form }: { form: LibraryFormController }) {
  const bt = useBloemT();
  if (!form.storageOffered) return null;
  const kinds = [
    ["paths", bt("bloem.admin.libraries.form.kind.folders")],
    ["storage", bt("bloem.admin.libraries.form.kind.storageSource")],
  ] as const;
  return (
    <div
      role="radiogroup"
      aria-label={bt("bloem.admin.libraries.form.kind.label")}
      className="flex gap-1.5"
    >
      {kinds.map(([kind, label]) => (
        <Button
          key={kind}
          type="button"
          role="radio"
          aria-checked={form.locationKind === kind}
          variant={form.locationKind === kind ? "secondary" : "outline"}
          size="sm"
          onClick={() => form.setLocationKind(kind)}
        >
          {label}
        </Button>
      ))}
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
      <Button type="button" variant="outline" onClick={() => void form.locations.reload()}>
        {bt("bloem.admin.libraries.form.reloadLocations")}
      </Button>
    </div>
  );
}

/**
 * What Silo's folder fields show instead of folders, when Bloem's library
 * locations or a storage source decide where the library reads from; null
 * when Silo's folder fields apply.
 */
export function bloemFolderFields(form: LibraryFormController): ReactNode | null {
  if (form.staleContext) return <StaleContextNotice />;
  if (form.locations.enabled) return <LibraryLocationsEditor form={form} />;
  if (form.locations.loading || form.locations.blocked) return <LocationsLoading form={form} />;
  if (form.usesStorage && form.library) return <StorageSourceFields form={form} />;
  if (form.usesStorage) {
    return (
      <div className="space-y-3">
        <LibraryLocationKindToggle form={form} />
        <StorageSourceFields form={form} />
      </div>
    );
  }
  return null;
}
