import { Library, ShieldCheck } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationLibraries } from "@/hooks/queries/admin/libraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function LibrariesEntitlementsPage() {
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const libraries = useOrganizationLibraries(contextKey);
  useDocumentTitle("Libraries & Entitlements");

  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Libraries &amp; Entitlements
          </h1>
          <p className="page-subtitle">
            Media available to {active?.name ?? "this organization"}, separated by ownership and
            Platform grants.
          </p>
        </div>
      </div>

      <div className="border-border bg-muted/30 rounded-xl border p-4 text-sm">
        <span className="font-semibold">
          Effective access is the intersection of this organization ceiling and profile/group
          restrictions.
        </span>{" "}
        <span className="text-muted-foreground">Both layers must allow the media.</span>
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
          {libraries.error.message}
        </div>
      )}
      {libraries.data?.length === 0 && (
        <p className="text-muted-foreground py-8 text-center text-sm">
          No libraries are inside this organization’s media ceiling.
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
                  {item.access_kind === "owned"
                    ? `Owned by ${active?.name ?? "organization"}`
                    : "Platform entitlement"}
                </Badge>
              </div>
              <h2 className="mt-5 text-lg font-semibold">{item.name}</h2>
              <p className="text-muted-foreground mt-1 text-sm capitalize">{item.type}</p>
              {item.entitlement && (
                <p className="text-muted-foreground mt-3 text-xs">
                  Entitlement {item.entitlement.status} · security revision{" "}
                  {item.entitlement.security_revision}
                </p>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
