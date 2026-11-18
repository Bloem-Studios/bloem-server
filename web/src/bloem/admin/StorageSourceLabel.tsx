import { useBloemT } from "@/bloem/i18n";

/**
 * Marks a library that reads a storage source rather than folders in the admin
 * library list. Silo gives every library at least one path, so a library with
 * none reads a storage source.
 */
export function StorageSourceLabel({ library }: { library: { paths: string[] } }) {
  const bt = useBloemT();
  if (library.paths.length > 0) return null;
  return (
    <span className="text-muted-foreground me-2">
      {bt("bloem.admin.libraries.form.storageSource")}
    </span>
  );
}
