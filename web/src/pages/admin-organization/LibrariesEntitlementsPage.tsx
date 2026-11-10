import { Library, ShieldCheck } from "lucide-react";

import type { AdminContextSummary } from "@/api/bloemTypes";
import { useBloemT } from "@/bloem/i18n";
import { libraryTypeLabel } from "@/components/admin/libraries/libraryTypes";
import { LibraryEntitlementActions } from "@/components/admin/organizations/LibraryEntitlementActions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function LibrariesEntitlementsPage() {
  const bt = useBloemT();
  const { active } = useAdminContext();
  useDocumentTitle(bt("bloem.admin.organization.libraries.title"));
  if (active?.scope !== "organization") {
    return <p role="alert">{bt("bloem.admin.organization.libraries.selectOrganization")}</p>;
  }
  return <OrganizationLibraries key={active.key} active={active} />;
}

function OrganizationLibraries({ active }: { active: AdminContextSummary }) {
  const bt = useBloemT();
  const libraries = useOrganizationLibraries(active.key);
  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            {bt("bloem.admin.organization.libraries.title")}
          </h1>
          <p className="page-subtitle">
            {bt("bloem.admin.organization.libraries.subtitle", {
              organization: active.name ?? bt("bloem.admin.organization.thisOrganization"),
            })}
          </p>
        </div>
      </div>

      <div className="border-border bg-muted/30 rounded-xl border p-4 text-sm">
        {bt("bloem.admin.organization.libraries.ceilingNote", {
          b: (chunks) => <span className="font-medium">{chunks}</span>,
          muted: (chunks) => <span className="text-muted-foreground">{chunks}</span>,
        })}
      </div>

      {libraries.isLoading && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
        </div>
      )}
      {libraries.isError && (
        <div
          className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
          role="alert"
        >
          <p>{bt("bloem.admin.organization.libraries.loadFailed")}</p>
          <Button variant="outline" className="mt-3" onClick={() => void libraries.refetch()}>
            {bt("bloem.admin.organization.libraries.tryAgain")}
          </Button>
        </div>
      )}
      {libraries.data?.length === 0 && (
        <p className="text-muted-foreground py-8 text-center text-sm">
          {bt("bloem.admin.organization.libraries.empty")}
        </p>
      )}
      {libraries.data && libraries.data.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {libraries.data.map((item) => (
            <article key={item.folder_id} className="surface-panel rounded-2xl p-5">
              <div className="flex items-start justify-between gap-3">
                {item.access_kind === "owned" ? (
                  <Library className="text-muted-foreground size-5" aria-hidden />
                ) : (
                  <ShieldCheck className="text-muted-foreground size-5" aria-hidden />
                )}
                <Badge variant={item.access_kind === "owned" ? "secondary" : "outline"}>
                  {item.access_kind !== "owned"
                    ? bt("bloem.admin.organization.libraries.sharedByServer")
                    : typeof active.name === "string"
                      ? bt("bloem.admin.organization.libraries.ownedBy", {
                          organization: active.name,
                        })
                      : bt("bloem.admin.organization.libraries.ownedByUnnamed")}
                </Badge>
              </div>
              <h2 className="mt-5 text-lg font-medium">{item.name}</h2>
              <p className="text-muted-foreground mt-1 text-sm capitalize">
                {libraryTypeLabel(item.type)}
              </p>
              {item.entitlement && (
                <p className="text-muted-foreground mt-3 text-xs">
                  {item.entitlement.status === "active"
                    ? bt("bloem.admin.organization.libraries.statusActive")
                    : item.entitlement.status === "suspended"
                      ? bt("bloem.admin.organization.libraries.statusSuspended")
                      : bt("bloem.admin.organization.libraries.statusRevoked")}
                </p>
              )}
              <LibraryEntitlementActions
                library={item}
                contextKey={active.key}
                disabled={libraries.isFetching || libraries.isError}
              />
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
