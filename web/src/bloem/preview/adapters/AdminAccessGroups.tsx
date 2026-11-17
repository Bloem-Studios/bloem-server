import SiloAdminAccessGroups from "@/pages/AdminAccessGroups";
import OrganizationAdminAccessGroups from "@/pages/OrganizationAdminAccessGroups";
import { useOptionalAdminContext } from "@/contexts/AdminContextProvider";

/**
 * The access groups page: an organization's own inside an organization
 * context, Silo's platform page otherwise. Outside an AdminContextProvider
 * (Silo's own page tests) it is Silo's page.
 */
export default function AdminAccessGroups() {
  const active = useOptionalAdminContext()?.active;
  return active?.scope === "organization" ? (
    <OrganizationAdminAccessGroups key={active.key} />
  ) : (
    <SiloAdminAccessGroups />
  );
}
