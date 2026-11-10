import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { buildLibraryCollectionCatalogHref } from "@/pages/catalogSearchParams";
import { v2 } from "@/api/v2/request";
import { useBloemT } from "@/bloem/i18n";
export default function PreviewCollections() {
  const bt = useBloemT();
  const { data, isPending, error } = useQuery({
    queryKey: ["bloem-preview-collections"],
    queryFn: () => v2("GET /api/v2/collections/server"),
    retry: false,
  });
  return (
    <main className="page-shell py-8">
      <h1 className="mb-6 text-3xl font-bold">{bt("bloem.preview.collections.title")}</h1>
      {isPending ? (
        <p role="status">{bt("bloem.preview.collections.loading")}</p>
      ) : error ? (
        <p role="alert">{bt("bloem.preview.collections.unavailable")}</p>
      ) : (
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          {data?.libraries
            .flatMap((library) =>
              library.collections.map((entry) => ({
                ...entry,
                libraryId: Number(library.library_id),
              })),
            )
            .map((entry) => (
              <Link
                key={entry.id}
                className="rounded-xl border p-5"
                to={buildLibraryCollectionCatalogHref(entry.id, entry.title, entry.libraryId)}
              >
                <h2 className="font-semibold">{entry.title}</h2>
                <p className="text-muted-foreground mt-2 text-sm">
                  {bt("bloem.preview.collections.titleCount", { count: entry.item_count })}
                </p>
              </Link>
            ))}
        </div>
      )}
    </main>
  );
}
