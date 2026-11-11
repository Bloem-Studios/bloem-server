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
        <div className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-6">
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
                className="group/card block"
                to={buildLibraryCollectionCatalogHref(entry.id, entry.title, entry.libraryId)}
              >
                {/* The same poster the Collections page shows; a guest gets no
                    pin or edit controls, so this card has none. */}
                <div className="media-card-image bg-muted relative aspect-[2/3] overflow-hidden rounded-xl">
                  {entry.poster_url ? (
                    <img
                      src={entry.poster_url}
                      alt=""
                      loading="lazy"
                      className="h-full w-full object-cover transition duration-300 group-hover/card:scale-[1.05]"
                    />
                  ) : null}
                </div>
                <h2 className="mt-2 truncate font-semibold">{entry.title}</h2>
                <p className="text-muted-foreground text-sm">
                  {bt("bloem.preview.collections.titleCount", { count: entry.item_count })}
                </p>
              </Link>
            ))}
        </div>
      )}
    </main>
  );
}
