import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { buildLibraryCollectionCatalogHref } from "@/pages/catalogSearchParams";
import { v2 } from "@/api/v2/request";
export default function PreviewCollections() {
  const { data, isPending, error } = useQuery({
    queryKey: ["bloem-preview-collections"],
    queryFn: () => v2("GET /api/v2/collections/server"),
    retry: false,
  });
  return (
    <main className="page-shell py-8">
      <h1 className="mb-6 text-3xl font-bold">Collections</h1>
      {isPending ? (
        <p role="status">Loading collections…</p>
      ) : error ? (
        <p role="alert">Collections are unavailable. Please try again.</p>
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
                <p className="text-muted-foreground mt-2 text-sm">{entry.item_count} titles</p>
              </Link>
            ))}
        </div>
      )}
    </main>
  );
}
