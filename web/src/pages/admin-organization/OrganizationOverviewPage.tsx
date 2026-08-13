import { Building2, Library, ShieldCheck, UserRound, Users } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { useOrganizationOverview } from "@/hooks/queries/admin/organizationPeople";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";

export default function OrganizationOverviewPage() {
  const { active } = useAdminContext();
  const contextKey = active?.key ?? "organization:unavailable";
  const overview = useOrganizationOverview(contextKey);
  useDocumentTitle(active?.name ?? "Organization overview");

  if (overview.isLoading) {
    return (
      <section className="admin-page space-y-5" role="status" aria-label="Loading organization">
        <Skeleton className="h-16 w-80 max-w-full" />
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className="h-28" />
          ))}
        </div>
      </section>
    );
  }
  if (overview.isError || !overview.data) {
    return (
      <div
        className="border-destructive/30 bg-destructive/10 text-destructive rounded-xl border p-4"
        role="alert"
      >
        {overview.error?.message ?? "Organization overview is unavailable."}
      </div>
    );
  }

  const organization = overview.data;
  const metrics = [
    { label: "People", value: organization.membership_count, icon: Users },
    { label: "Profiles", value: organization.profile_count, icon: UserRound },
    { label: "Owned libraries", value: organization.library_count, icon: Library },
    { label: "Entitlements", value: organization.entitlement_count, icon: ShieldCheck },
  ];
  return (
    <section className="admin-page space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <div className="flex items-center gap-3">
            <Building2 className="text-muted-foreground h-6 w-6" />
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
              {organization.name}
            </h1>
          </div>
          <p className="page-subtitle">
            Organization health, media scope, and authorization state.
          </p>
        </div>
        <Badge variant={organization.status === "active" ? "secondary" : "destructive"}>
          {organization.status}
        </Badge>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {metrics.map(({ label, value, icon: Icon }) => (
          <article key={label} className="surface-panel rounded-2xl p-5">
            <Icon className="text-muted-foreground h-5 w-5" />
            <p className="mt-5 text-3xl font-semibold tabular-nums">{value.toLocaleString()}</p>
            <p className="text-muted-foreground mt-1 text-sm">{label}</p>
          </article>
        ))}
      </div>

      <div className="surface-panel rounded-2xl p-5">
        <h2 className="font-semibold">Authorization state</h2>
        <dl className="mt-4 grid gap-4 text-sm sm:grid-cols-3">
          <div>
            <dt className="text-muted-foreground">Policy</dt>
            <dd className="mt-1 font-medium">Revision {organization.policy_revision}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Organization slug</dt>
            <dd className="mt-1 font-medium">{organization.slug}</dd>
          </div>
          <div>
            <dt className="text-muted-foreground">Owner account</dt>
            <dd className="mt-1 font-medium">{organization.owner_account_id ?? "Not assigned"}</dd>
          </div>
        </dl>
      </div>
    </section>
  );
}
