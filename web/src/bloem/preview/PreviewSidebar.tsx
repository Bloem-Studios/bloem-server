import { Link } from "react-router";
import { Film, Search, Library, Home, CalendarDays, FolderOpen } from "lucide-react";
import { SiloBrand } from "@/components/SiloBrand";
import { useBloemT } from "@/bloem/i18n";
import { useUserLibraries } from "@/hooks/queries/libraries";
export default function PreviewSidebar({
  onNavigate,
  collapsed = false,
}: {
  onNavigate?: () => void;
  collapsed?: boolean;
}) {
  const bt = useBloemT();
  const { data: libraries } = useUserLibraries();
  const row = "hover:bg-sidebar-accent flex items-center gap-3 rounded-xl px-3 py-3 text-sm";
  return (
    <aside
      className={`bg-sidebar text-sidebar-foreground fixed start-0 top-[var(--bloem-preview-banner-height,49px)] bottom-0 z-40 flex flex-col overflow-y-auto border-e p-3 ${collapsed ? "w-16" : "w-64"}`}
    >
      <SiloBrand />
      <nav aria-label={bt("bloem.preview.sidebar.navigation")} className="mt-6 flex-1 space-y-1">
        {[
          ["/", bt("bloem.preview.sidebar.home"), Home],
          ["/catalog", bt("bloem.preview.sidebar.search"), Search],
          ["/collections", bt("bloem.preview.sidebar.collections"), FolderOpen],
          ["/calendar", bt("bloem.preview.sidebar.calendar"), CalendarDays],
        ].map(([href, label, Icon]) => {
          const Symbol = Icon as typeof Home;
          return (
            <Link
              key={href as string}
              to={href as string}
              aria-label={label as string}
              title={label as string}
              className={row}
              onClick={onNavigate}
            >
              <Symbol className="size-5 shrink-0" />
              {!collapsed && <span>{label as string}</span>}
            </Link>
          );
        })}
        {!collapsed && (
          <p className="text-muted-foreground px-3 pt-6 pb-2 text-xs uppercase">
            {bt("bloem.preview.sidebar.libraries")}
          </p>
        )}
        {libraries?.map((library) => (
          <Link
            key={library.id}
            to={`/library/${library.id}`}
            aria-label={library.name}
            title={library.name}
            className={row}
            onClick={onNavigate}
          >
            {library.type === "movies" ? (
              <Film className="size-5 shrink-0" />
            ) : (
              <Library className="size-5 shrink-0" />
            )}
            {!collapsed && <span className="truncate">{library.name}</span>}
          </Link>
        ))}
      </nav>
      <p className="text-muted-foreground px-3 py-3 text-xs">
        {collapsed ? bt("bloem.preview.sidebar.preview") : bt("bloem.preview.browseOnly")}
      </p>
    </aside>
  );
}
