import { Library, ShieldCheck } from "lucide-react";

import type { AdminContextSummary } from "@/api/bloemTypes";
import { LibraryEntitlementActions } from "@/components/admin/organizations/LibraryEntitlementActions";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationLibraries } from "@/hooks/queries/admin/organizationLibraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function LibrariesEntitlementsPage() {
  const { active } = useAdminContext();
  useDocumentTitle("Libraries");
  if (active?.scope !== "organization") {
    return <p role="alert">Select an organization to manage its libraries.</p>;
  }
  return <OrganizationLibraries key={active.key} active={active} />;
}

function OrganizationLibraries({ active }: { active: AdminContextSummary }) {
  const libraries = useOrganizationLibraries(active.key);
  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
            Libraries
          </h1>
          <p className="page-subtitle">
            Libraries {active?.name ?? "this organization"} owns, and libraries the server shares
            with it.
          </p>
        </div>
      </div>

      <div className="border-border bg-muted/30 rounded-xl border p-4 text-sm">
        <span className="font-medium">These are the most this organization’s members can see.</span>{" "}
        <span className="text-muted-foreground">
          Each person’s plan, access group and profile can narrow it further.
        </span>
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
          <p>Libraries couldn’t be loaded.</p>
          <Button variant="outline" className="mt-3" onClick={() => void libraries.refetch()}>
            Try again
          </Button>
        </div>
      )}
      {libraries.data?.length === 0 && (
        <p className="text-muted-foreground py-8 text-center text-sm">
          This organization has no libraries yet.
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
                    : "Shared by the server"}
                </Badge>
              </div>
              <h2 className="mt-5 text-lg font-medium">{item.name}</h2>
              <p className="text-muted-foreground mt-1 text-sm capitalize">{item.type}</p>
              {item.entitlement && (
                <p className="text-muted-foreground mt-3 text-xs">
                  {item.entitlement.status === "active"
                    ? "Members can use it"
                    : item.entitlement.status === "suspended"
                      ? "Paused: members can’t use it until it is restored"
                      : "Withdrawn"}
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
