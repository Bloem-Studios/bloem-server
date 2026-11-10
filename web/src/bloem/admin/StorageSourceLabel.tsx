import { useBloemT } from "@/bloem/i18n";

/** Marks a library that reads a storage source rather than folders in the admin library list. */
export function StorageSourceLabel({ library }: { library: { storage_source?: string } }) {
  const bt = useBloemT();
  if (!library.storage_source) return null;
  return (
    <span className="text-muted-foreground me-2">
      {bt("bloem.admin.libraries.form.storageSource")}
    </span>
  );
}
