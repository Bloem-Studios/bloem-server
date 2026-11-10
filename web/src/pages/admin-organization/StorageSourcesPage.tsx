import type { AdminContextSummary } from "@/api/bloemTypes";
import { useBloemT } from "@/bloem/i18n";
import { StorageSourcesPanel } from "@/components/admin/plugins/StorageSourcesPanel";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useStorageSources } from "@/hooks/queries/admin/storageSources";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

/** The server answers 403 or 404 when the organization may not use storage sources at all. */
function isUnavailable(error: unknown): boolean {
  if (typeof error !== "object" || error === null || !("status" in error)) return false;
  const status = (error as { status?: unknown }).status;
  return status === 403 || status === 404;
}

/**
 * `/admin/organization/storage`: the organization's own storage sources. Only
 * the sources panel is shown; the rest of the Plugins page is platform-only.
 */
export default function StorageSourcesPage() {
  const bt = useBloemT();
  const { active } = useAdminContext();
  useDocumentTitle(bt("bloem.admin.organization.storage.title"));
  if (active?.scope !== "organization") {
    return <p role="alert">{bt("bloem.admin.organization.storage.selectOrganization")}</p>;
  }
  return <OrganizationStorageSources key={active.key} active={active} />;
}

function OrganizationStorageSources({ active }: { active: AdminContextSummary }) {
  const bt = useBloemT();
  // Shares its cache entry with the panel, so this adds no request.
  const sources = useStorageSources(true);
  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.organization.storage.title")}
          </h1>
          <p className="page-subtitle">
            {bt("bloem.admin.organization.storage.subtitle", {
              organization: active.name ?? bt("bloem.admin.organization.thisOrganization"),
            })}
          </p>
        </div>
      </div>

      {isUnavailable(sources.error) ? (
        <p role="status" className="text-muted-foreground py-8 text-center text-sm">
          {bt("bloem.admin.organization.storage.notEnabled")}
        </p>
      ) : (
        <StorageSourcesPanel />
      )}
    </section>
  );
}
